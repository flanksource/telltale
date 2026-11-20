package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.Arrays;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

/**
 * Covers which JDK the out-of-process attach is launched from. Both halves of the Attach API —
 * tools.jar and libattach.so — have to come from the same JDK, so what is probed, and in what
 * order, is the whole correctness story here.
 */
final class InstrumentationServiceTest {

  private static final String JDK_HOME_PROPERTY = "telltale.diag.jdk-home";

  @Test
  void doesNotAssumeAnApplicationSpecificSideloadedJdk() {
    List<Path> candidates = InstrumentationService.jdkHomeCandidates();
    assertTrue(
        !candidates.contains(Paths.get("/tmp/jdk")),
        "JDK selection must use explicit configuration or the runtime environment: " + candidates);
  }

  @Test
  void probesTheJvmsOwnHome() {
    List<Path> candidates = InstrumentationService.jdkHomeCandidates();
    Path javaHome = Paths.get(System.getProperty("java.home"));

    assertTrue(candidates.indexOf(javaHome) >= 0, "java.home must be probed: " + candidates);
  }

  /** A JDK 8 process reports java.home as {@code <jdk>/jre}, where tools.jar is not. */
  @Test
  void probesTheParentOfJavaHomeForTheJava8JreLayout() {
    List<Path> candidates = InstrumentationService.jdkHomeCandidates();
    Path parent = Paths.get(System.getProperty("java.home"), "..").normalize();

    assertTrue(candidates.contains(parent), "the <jdk>/jre parent must be probed: " + candidates);
  }

  @Test
  void theExplicitOverrideWinsOverEveryProbedLocation() {
    String previous = System.getProperty(JDK_HOME_PROPERTY);
    System.setProperty(JDK_HOME_PROPERTY, "/opt/custom/jdk");
    try {
      List<Path> candidates = InstrumentationService.jdkHomeCandidates();
      assertEquals(
          Paths.get("/opt/custom/jdk"),
          candidates.get(0),
          "an operator's -D override must be tried first: " + candidates);
    } finally {
      if (previous == null) {
        System.clearProperty(JDK_HOME_PROPERTY);
      } else {
        System.setProperty(JDK_HOME_PROPERTY, previous);
      }
    }
  }

  /**
   * The command must put the chosen JDK's own tools.jar on the classpath and run its own launcher:
   * mixing a JRE launcher with someone else's tools.jar is the exact failure this design exists to
   * prevent.
   */
  @Test
  void theAttachCommandPairsTheLauncherWithItsOwnToolsJar() {
    Path home = Paths.get("/tmp/jdk");
    InstrumentationService.Jdk jdk =
        new InstrumentationService.Jdk(
            home, home.resolve("bin/java"), home.resolve("lib/tools.jar"));

    List<String> command =
        jdk.attachCommand("1", Paths.get("/tmp/telltale-diag/telltale-diag-agent.jar"));

    assertEquals("/tmp/jdk/bin/java", command.get(0));
    assertEquals("-cp", command.get(1));
    assertEquals(
        "/tmp/jdk/lib/tools.jar:/tmp/telltale-diag/telltale-diag-agent.jar", command.get(2));
    assertEquals("com.flanksource.telltale.diag.agent.AttachMain", command.get(3));
    assertEquals("1", command.get(4), "the target pid is this JVM's");
    assertEquals("/tmp/telltale-diag/telltale-diag-agent.jar", command.get(5));
  }

  /** Java 9+ has the attach API in the base modules, so no tools.jar goes on the classpath. */
  @Test
  void theAttachCommandOmitsToolsJarWhenTheJdkDoesNotNeedIt() {
    Path home = Paths.get("/opt/java/openjdk");
    InstrumentationService.Jdk jdk =
        new InstrumentationService.Jdk(home, home.resolve("bin/java"), null);

    List<String> command = jdk.attachCommand("1", Paths.get("/tmp/agent.jar"));

    assertEquals("/tmp/agent.jar", command.get(2));
  }

  /**
   * The application image exports JAVA_TOOL_OPTIONS carrying its own -javaagent pair. Inheriting it
   * makes the attacher abort trying to bind the JMX exporter port this JVM already holds, so it
   * never reaches the attach at all.
   */
  @Test
  void theAttacherDoesNotInheritTheContainersJvmOptions() {
    Map<String, String> environment = new HashMap<>();
    environment.put(
        "JAVA_TOOL_OPTIONS",
        "-javaagent:/etc/jmx-exporter/jmx_prometheus_javaagent.jar=12345:/etc/c.yaml");
    environment.put("_JAVA_OPTIONS", "-Xmx1g");
    environment.put("JDK_JAVA_OPTIONS", "--add-opens=java.base/java.lang=ALL-UNNAMED");
    environment.put("JAVA_HOME", "/usr/local/openjdk-8");
    environment.put("PATH", "/usr/bin");

    InstrumentationService.stripInheritedJvmOptions(environment);

    assertEquals(
        new HashSet<>(Arrays.asList("JAVA_HOME", "PATH")),
        environment.keySet(),
        "only the JVM-option carriers may be stripped");
  }
}
