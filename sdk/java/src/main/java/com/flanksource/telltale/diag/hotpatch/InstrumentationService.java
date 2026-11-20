package com.flanksource.telltale.diag.hotpatch;

import java.io.File;
import java.io.IOException;
import java.io.InputStream;
import java.lang.instrument.Instrumentation;
import java.lang.management.ManagementFactory;
import java.lang.reflect.Field;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.nio.file.StandardCopyOption;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;

/**
 * Obtains {@link Instrumentation} for the JVM this WAR is running inside, by launching a
 * short-lived JDK process that attaches back to us and loads {@link
 * com.flanksource.telltale.diag.agent.DiagAgent}.
 *
 * <p>The attach happens out-of-process because the Attach API has two halves that must come from
 * the <em>same</em> JDK: {@code tools.jar} (classes) and {@code libattach.so} (native transport,
 * resolved through {@code java.library.path}). application images are frequently JRE-only, which
 * supplies neither; loading a side-loaded {@code tools.jar} into an in-process classloader supplies
 * only the first half and dies with {@code UnsatisfiedLinkError: no attach in java.library.path}.
 * Launching the attach from a complete JDK's own {@code bin/java} makes both halves line up, and as
 * a bonus is not self-attach, so Java 9+ needs no {@code jdk.attach.allowAttachSelf}.
 *
 * <p>The result is cached. So is a failure: every failure mode here is structural (no usable JDK,
 * missing agent jar, a SecurityManager, a JVM that refuses attach), so retrying per request would
 * only cost an attach round-trip to produce the same message. A webapp redeploy builds a new
 * classloader and therefore retries from scratch.
 */
public final class InstrumentationService {

  /** Holder class parked on the system class path by the attached agent. */
  public static final String AGENT_CLASS = "com.flanksource.telltale.diag.agent.DiagAgent";

  /**
   * The {@code diagnosticAgentJar} build, nested whole into telltale-agent.jar by the {@code
   * agentJar} Gradle task (a plain Jar, so it is not unpacked the way the shadow plugin would).
   */
  public static final String AGENT_RESOURCE = "/telltale-diag-agent.jar";

  /** Main class of {@link #AGENT_RESOURCE}, run by the attaching JDK process. */
  private static final String ATTACH_MAIN = "com.flanksource.telltale.diag.agent.AttachMain";

  /** Operator override naming the JDK to attach with; probed before everything else. */
  private static final String JDK_HOME_PROPERTY = "telltale.diag.jdk-home";

  /** How long the attaching process gets before we give up on it. */
  private static final long ATTACH_TIMEOUT_SECONDS = 60;

  /** Grace period for the output drain once the attacher has exited or been killed. */
  private static final long DRAIN_TIMEOUT_MILLIS = 2000;

  private static Instrumentation cached;
  private static String failure;

  private InstrumentationService() {}

  public static boolean installed() {
    return fromHolder() != null;
  }

  /**
   * @return the live {@link Instrumentation} for this JVM.
   * @throws IllegalStateException with a mode-specific message when it cannot be obtained.
   */
  public static synchronized Instrumentation instrumentation() {
    if (cached != null) {
      return cached;
    }
    if (failure != null) {
      throw new IllegalStateException(failure);
    }
    try {
      cached = attach();
      return cached;
    } catch (IllegalStateException e) {
      failure = e.getMessage();
      throw e;
    }
  }

  /** Contract §4 {@code instrumentation} block for the selftest response. */
  public static Map<String, Object> status() {
    Map<String, Object> out = new LinkedHashMap<>();
    try {
      Instrumentation inst = instrumentation();
      out.put("available", true);
      out.put("retransformSupported", inst.isRetransformClassesSupported());
      out.put("redefineSupported", inst.isRedefineClassesSupported());
      out.put("error", "");
    } catch (RuntimeException e) {
      out.put("available", false);
      out.put("retransformSupported", false);
      out.put("redefineSupported", false);
      out.put("error", e.getMessage() == null ? String.valueOf(e) : e.getMessage());
    }
    return out;
  }

  private static Instrumentation attach() {
    Instrumentation existing = fromHolder();
    if (existing != null) {
      return existing;
    }
    String pid = pid();
    Path agentJar = stageAgentJar();
    Jdk jdk = findJdk();

    List<String> command = jdk.attachCommand(pid, agentJar);
    String output = run(command);

    Instrumentation loaded = fromHolder();
    if (loaded == null) {
      throw new IllegalStateException(
          "agent "
              + agentJar.toAbsolutePath()
              + " was loaded into pid "
              + pid
              + " by "
              + jdk.javaBin
              + " but "
              + AGENT_CLASS
              + ".INSTRUMENTATION is still null"
              + " (a stale agent jar without an agentmain hook is on the system class path)."
              + " Attacher said: "
              + output);
    }
    return loaded;
  }

  /**
   * Environment variables through which the container injects JVM flags. The application image sets
   * JAVA_TOOL_OPTIONS with its own {@code -javaagent} pair (Prometheus JMX exporter,
   * OpenTelemetry). A child JVM inherits them, tries to bind the exporter port this JVM already
   * owns, and aborts with "processing of -javaagent failed" before it ever attaches — so the
   * attacher must run as a bare JVM.
   */
  private static final List<String> INHERITED_JVM_OPTION_VARS =
      Arrays.asList("JAVA_TOOL_OPTIONS", "_JAVA_OPTIONS", "JDK_JAVA_OPTIONS");

  /** Package-visible for tests; see {@link #INHERITED_JVM_OPTION_VARS}. */
  static void stripInheritedJvmOptions(Map<String, String> environment) {
    environment.keySet().removeAll(INHERITED_JVM_OPTION_VARS);
  }

  /** Runs the attaching JDK process, failing loud with its own output. */
  private static String run(List<String> command) {
    Process process;
    try {
      ProcessBuilder builder = new ProcessBuilder(command).redirectErrorStream(true);
      stripInheritedJvmOptions(builder.environment());
      process = builder.start();
    } catch (IOException e) {
      throw new IllegalStateException(
          "attach refused: could not run " + command + ": " + describe(e), e);
    } catch (SecurityException e) {
      throw securityManagerFailure("launch the attaching JDK process", e);
    }
    // Drain on another thread: reading to EOF on this one would block past
    // ATTACH_TIMEOUT_SECONDS if the child hangs without closing stdout,
    // which is exactly the case the timeout exists for.
    StringBuilder sink = new StringBuilder();
    Thread drain =
        new Thread(
            () -> sink.append(readFully(process.getInputStream())), "telltale-diag-attach-output");
    drain.setDaemon(true);
    drain.start();

    boolean exited;
    try {
      exited = process.waitFor(ATTACH_TIMEOUT_SECONDS, TimeUnit.SECONDS);
      if (!exited) {
        process.destroyForcibly();
      }
      drain.join(DRAIN_TIMEOUT_MILLIS);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      process.destroyForcibly();
      throw new IllegalStateException("attach interrupted while waiting for " + command, e);
    }
    String output = sink.toString();
    if (!exited) {
      throw new IllegalStateException(
          "attach timed out: "
              + command
              + " did not finish within "
              + ATTACH_TIMEOUT_SECONDS
              + "s. Output so far: "
              + output);
    }
    if (process.exitValue() != 0) {
      throw new IllegalStateException(
          "attach refused: "
              + command
              + " exited "
              + process.exitValue()
              + ". The JVM may have been started with -XX:+DisableAttachMechanism,"
              + " or the attaching process may not share this process' user. Output: "
              + output);
    }
    return output;
  }

  private static String readFully(InputStream in) {
    StringBuilder out = new StringBuilder();
    byte[] buffer = new byte[4096];
    try {
      for (int read; (read = in.read(buffer)) != -1; ) {
        out.append(new String(buffer, 0, read, StandardCharsets.UTF_8));
      }
    } catch (IOException e) {
      out.append("<could not read attacher output: ").append(describe(e)).append('>');
    }
    return out.toString().trim();
  }

  /**
   * A JDK complete enough to attach with: a {@code bin/java} launcher, plus that same JDK's {@code
   * tools.jar} on Java 8. Pairing the two is the whole point — a JRE launcher cannot find {@code
   * libattach.so} for someone else's tools.jar.
   */
  static final class Jdk {
    final Path home;
    final Path javaBin;

    /** null on Java 9+, where the attach API is in the base modules. */
    final Path toolsJar;

    Jdk(Path home, Path javaBin, Path toolsJar) {
      this.home = home;
      this.javaBin = javaBin;
      this.toolsJar = toolsJar;
    }

    List<String> attachCommand(String pid, Path agentJar) {
      String classpath = agentJar.toAbsolutePath().toString();
      if (toolsJar != null) {
        classpath = toolsJar + File.pathSeparator + classpath;
      }
      return Arrays.asList(
          javaBin.toString(),
          "-cp",
          classpath,
          ATTACH_MAIN,
          pid,
          agentJar.toAbsolutePath().toString());
    }
  }

  /**
   * @return a JDK complete enough to attach with — and, for {@link CompilerService}, to compile
   *     with: the same tools.jar carries javac on Java 8.
   */
  static Jdk findJdk() {
    boolean needsToolsJar = needsToolsJar();
    for (Path home : jdkHomeCandidates()) {
      Path javaBin = home.resolve("bin").resolve("java");
      if (!Files.isExecutable(javaBin)) {
        continue;
      }
      if (!needsToolsJar) {
        return new Jdk(home, javaBin, null);
      }
      Path toolsJar = home.resolve("lib").resolve("tools.jar");
      if (Files.isReadable(toolsJar)) {
        return new Jdk(home, javaBin, toolsJar);
      }
    }
    throw new IllegalStateException(
        "no JDK available to attach with"
            + " (java.version="
            + System.getProperty("java.version")
            + ", java.home="
            + System.getProperty("java.home")
            + ")."
            + " Needed a directory with bin/java"
            + (needsToolsJar ? " and lib/tools.jar" : "")
            + "; probed "
            + jdkHomeCandidates()
            + ". Run the application on a full JDK image or point -D"
            + JDK_HOME_PROPERTY
            + " at a matching JDK.");
  }

  /** Java 8 keeps the attach API in tools.jar; 9+ ships it in the base modules. */
  private static boolean needsToolsJar() {
    String spec = System.getProperty("java.specification.version", "");
    return spec.startsWith("1.");
  }

  /**
   * @return every JDK home probed, in priority order. Package-visible so tests can assert the
   *     ordering, which is load-bearing: a JDK in the image must win over the side-loaded one.
   */
  static List<Path> jdkHomeCandidates() {
    List<Path> candidates = new ArrayList<>();
    String override = System.getProperty(JDK_HOME_PROPERTY);
    if (override != null && !override.trim().isEmpty()) {
      candidates.add(Paths.get(override.trim()));
    }
    // A JDK 8 process reports java.home as <jdk>/jre, so the JDK sits one level up.
    String javaHome = System.getProperty("java.home");
    if (javaHome != null && !javaHome.isEmpty()) {
      candidates.add(Paths.get(javaHome));
      candidates.add(Paths.get(javaHome, "..").normalize());
    }
    String env = System.getenv("JAVA_HOME");
    if (env != null && !env.isEmpty()) {
      candidates.add(Paths.get(env));
    }
    return candidates;
  }

  private static Path stageAgentJar() {
    Path target =
        Paths.get(System.getProperty("java.io.tmpdir"), "telltale-diag", "telltale-diag-agent.jar");
    try (InputStream in = InstrumentationService.class.getResourceAsStream(AGENT_RESOURCE)) {
      if (in == null) {
        throw new IllegalStateException(
            "agent jar missing: "
                + AGENT_RESOURCE
                + " is not bundled in this WAR — rebuild the WAR so the Gradle agentJar task runs");
      }
      Files.createDirectories(target.getParent());
      Files.copy(in, target, StandardCopyOption.REPLACE_EXISTING);
    } catch (IOException e) {
      throw new IllegalStateException(
          "agent jar missing: could not stage "
              + AGENT_RESOURCE
              + " to "
              + target
              + ": "
              + describe(e),
          e);
    } catch (SecurityException e) {
      throw securityManagerFailure("stage the agent jar to " + target, e);
    }
    return target;
  }

  private static Instrumentation fromHolder() {
    try {
      Class<?> holder = ClassLoader.getSystemClassLoader().loadClass(AGENT_CLASS);
      Field field = holder.getField("INSTRUMENTATION");
      return (Instrumentation) field.get(null);
    } catch (ClassNotFoundException e) {
      return null;
    } catch (SecurityException e) {
      throw securityManagerFailure("read " + AGENT_CLASS + ".INSTRUMENTATION", e);
    } catch (ReflectiveOperationException e) {
      throw new IllegalStateException(
          "agent holder "
              + AGENT_CLASS
              + " is on the system class path but unreadable: "
              + describe(e)
              + " (a different telltale-diag agent jar was attached earlier)",
          e);
    }
  }

  private static String pid() {
    String name = ManagementFactory.getRuntimeMXBean().getName();
    String pid = name.split("@")[0];
    if (pid.isEmpty() || !pid.matches("\\d+")) {
      throw new IllegalStateException(
          "attach refused: RuntimeMXBean name \"" + name + "\" does not start with a numeric pid");
    }
    return pid;
  }

  private static IllegalStateException securityManagerFailure(
      String what, SecurityException cause) {
    SecurityManager securityManager = System.getSecurityManager();
    if (securityManager == null) {
      return new IllegalStateException(
          "access denied trying to "
              + what
              + ": "
              + describe(cause)
              + " (no SecurityManager is installed, so this is a platform-level restriction)",
          cause);
    }
    return new IllegalStateException(
        "SecurityManager present: "
            + securityManager.getClass().getName()
            + " denied "
            + what
            + " — "
            + describe(cause)
            + ". Grant the telltale-diag webapp AllPermission or start the JVM without"
            + " -Djava.security.manager",
        cause);
  }

  private static String describe(Throwable t) {
    if (t == null) {
      return "unknown cause";
    }
    return t.getMessage() == null
        ? t.getClass().getName()
        : t.getClass().getName() + ": " + t.getMessage();
  }
}
