package com.flanksource.telltale.diag.hotpatch;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collection;
import java.util.Collections;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * What a classloader <em>is</em>, as opposed to what class implements it.
 *
 * <p>A TomEE JVM running application reports thousands of loaders, and the raw class name tells an
 * operator nothing: {@code URLClassLoader} and {@code AgentClassLoader} are not answers to "which
 * one holds the code I want to decompile". Worse, two families are pure noise — one loader per
 * JIT-generated reflection accessor, and one per business rule application compiles at runtime —
 * and between them they account for over 2,000 of the ~2,170 rows.
 *
 * <p>So each loader gets a {@link #getLabel() label} (what it is), a {@link #getKind() kind} (which
 * drives ordering, so application code sorts above platform noise), and optionally a {@link
 * #getGroup() group} — a virtual loader id that collapses a whole family into one row.
 *
 * <p>The rules are matched on the loader's class name first and its webapp context second. Class
 * name first is also what keeps this cheap: the two noisy families are recognised without the
 * reflective context probe that {@link ClassIndex#contextOf} performs.
 */
public final class LoaderProfile {

  /** Application code — what someone opening the class browser came for. */
  public static final String KIND_APP = "app";

  /** This diagnostic tool's own WAR. */
  public static final String KIND_TOOL = "tool";

  /** The servlet container and the shared libraries it publishes. */
  public static final String KIND_CONTAINER = "container";

  /** Some other deployed web application. */
  public static final String KIND_WEBAPP = "webapp";

  /** Bytecode generated at runtime rather than shipped in a JAR. */
  public static final String KIND_GENERATED = "generated";

  /** A java agent, loaded in isolation from the application. */
  public static final String KIND_AGENT = "agent";

  /** The JDK itself. */
  public static final String KIND_JDK = "jdk";

  /** No rule matched. */
  public static final String KIND_UNKNOWN = "unknown";

  /**
   * Virtual loader ids are {@code ~}-prefixed. {@code ~} is unreserved in RFC 3986 so it survives a
   * query string untouched, it cannot collide with an identity hash ({@code [0-9a-f]+}, bootstrap
   * {@code "0"}), and unlike {@code :} it cannot collide with the {@code <loader>:<class>} keys the
   * web UI builds. {@code @} is unusable here: the CLI layer in front of this WAR reads a leading
   * {@code @} as "load the value from this file".
   */
  static final String GROUP_PREFIX = "~";

  public static final String GROUP_COMPILED = GROUP_PREFIX + "compiled";
  public static final String GROUP_JDK_REFLECTION = GROUP_PREFIX + "jdk-reflection";
  public static final String GROUP_JMX_RMI = GROUP_PREFIX + "jmx-rmi";

  /** How many library names a description lists before it stops. */
  private static final int DESCRIPTION_FACETS = 4;

  private static final LoaderProfile BOOTSTRAP = new LoaderProfile("JDK bootstrap", KIND_JDK, null);

  /**
   * Loader class name to profile. Matched before the webapp context, because these are the loaders
   * that have no context and probing for one is the expensive part.
   */
  private static final Map<String, LoaderProfile> BY_CLASS_NAME = byClassName();

  private static Map<String, LoaderProfile> byClassName() {
    Map<String, LoaderProfile> rules = new LinkedHashMap<>();
    rules.put(
        "org.codehaus.janino.ByteArrayClassLoader",
        new LoaderProfile("Runtime compiled classes", KIND_GENERATED, GROUP_COMPILED));
    rules.put(
        "sun.reflect.DelegatingClassLoader",
        new LoaderProfile("JDK reflection accessors", KIND_JDK, GROUP_JDK_REFLECTION));
    rules.put(
        "javax.management.remote.rmi.NoCallStackClassLoader",
        new LoaderProfile("JMX RMI connector", KIND_JDK, GROUP_JMX_RMI));
    rules.put(
        "io.opentelemetry.javaagent.bootstrap.AgentClassLoader",
        new LoaderProfile("OpenTelemetry agent", KIND_AGENT, null));
    rules.put(
        "net.bytebuddy.utility.dispatcher.JavaDispatcher$DynamicClassLoader",
        new LoaderProfile("ByteBuddy dispatcher", KIND_AGENT, null));
    rules.put(
        "sun.misc.Launcher$AppClassLoader",
        new LoaderProfile("JVM classpath + java agents", KIND_CONTAINER, null));
    rules.put(
        "jdk.internal.loader.ClassLoaders$AppClassLoader",
        new LoaderProfile("JVM classpath + java agents", KIND_CONTAINER, null));
    rules.put(
        "sun.misc.Launcher$ExtClassLoader",
        new LoaderProfile("JDK extensions (lib/ext)", KIND_JDK, null));
    rules.put(
        "jdk.internal.loader.ClassLoaders$PlatformClassLoader",
        new LoaderProfile("JDK platform classes", KIND_JDK, null));
    rules.put(
        "sun.reflect.misc.MethodUtil",
        new LoaderProfile("JDK introspection trampoline", KIND_JDK, null));
    return Collections.unmodifiableMap(rules);
  }

  /**
   * Deliberately absent from {@link #BY_CLASS_NAME}: {@code java.net.URLClassLoader}. On the lab
   * application JVM it happens to be TomEE's shared {@code lib/} loader, but that is a fact about
   * one deployment rather than about the class, and naming it "TomEE common" would be a guess
   * presented as a label. It takes the fallback and is described by its contents instead, which
   * says EclipseLink/Coherence/OpenEJB/ActiveMQ/CXF — the part that is actually true and actually
   * useful.
   */
  private static final String CONTEXT_TOOL = "/telltale-diag";

  /**
   * Ordering: application code first, platform noise last.
   *
   * <p>Unknown outranks the JDK deliberately. Every JDK loader worth naming is classified
   * explicitly, so a loader that matched nothing is by construction <em>not</em> one of them — it
   * is far more likely to be application or container infrastructure. Sorting it below the
   * bootstrap loader would bury the largest unclassified loader in a TomEE JVM (the shared {@code
   * lib/} one, 8,000+ classes) at the bottom of the list.
   */
  private static final List<String> KIND_ORDER =
      Collections.unmodifiableList(
          Arrays.asList(
              KIND_APP,
              KIND_TOOL,
              KIND_CONTAINER,
              KIND_WEBAPP,
              KIND_GENERATED,
              KIND_AGENT,
              KIND_UNKNOWN,
              KIND_JDK));

  /**
   * Package prefix to the name a human would use for it. Most specific first — lookup takes the
   * first match, so {@code io.prometheus.jmx} must precede a hypothetical {@code io.prometheus}.
   *
   * <p>This is a <em>package</em> table rather than a loader table on purpose: it is the reusable
   * half. An unmapped package degrades to its first two segments rather than to nothing.
   */
  private static final Map<String, String> LIBRARY_NAMES = libraryNames();

  private static Map<String, String> libraryNames() {
    Map<String, String> names = new LinkedHashMap<>();
    names.put("com.flanksource.telltale.diag", "telltale-diag");
    names.put("org.benf.cfr", "CFR decompiler");
    names.put("org.eclipse.persistence", "EclipseLink");
    names.put("com.tangosol", "Coherence");
    names.put("org.apache.openejb", "OpenEJB");
    names.put("org.apache.tomee", "TomEE");
    names.put("org.apache.catalina", "Tomcat");
    names.put("org.apache.tomcat", "Tomcat");
    names.put("org.apache.juli", "Tomcat logging");
    names.put("org.apache.activemq", "ActiveMQ");
    names.put("org.apache.cxf", "CXF");
    names.put("org.apache.bval", "BVal");
    names.put("org.apache.xbean", "XBean");
    names.put("org.apache.commons", "Commons");
    names.put("org.glassfish.jersey", "Jersey");
    names.put("org.glassfish.hk2", "HK2");
    names.put("com.fasterxml.jackson", "Jackson");
    names.put("net.sf.saxon", "Saxon");
    names.put("org.codehaus.janino", "Janino");
    names.put("org.springframework", "Spring");
    names.put("reactor.core", "Reactor");
    names.put("org.aspectj", "AspectJ");
    names.put("com.microsoft.sqlserver", "MSSQL JDBC");
    names.put("io.opentelemetry", "OpenTelemetry");
    names.put("net.bytebuddy", "ByteBuddy");
    names.put("org.objectweb.asm", "ASM");
    names.put("okhttp3", "OkHttp");
    names.put("io.prometheus.jmx", "Prometheus JMX exporter");
    names.put("co.elastic.logging", "Elastic logging");
    names.put("org.slf4j", "SLF4J");
    names.put("org.apache.logging", "Log4j");
    return Collections.unmodifiableMap(names);
  }

  private final String label;
  private final String kind;
  private final String group;
  private final String context;

  LoaderProfile(String label, String kind, String group) {
    this(label, kind, group, null);
  }

  private LoaderProfile(String label, String kind, String group, String context) {
    this.label = label;
    this.kind = kind;
    this.group = group;
    this.context = context;
  }

  public String getLabel() {
    return label;
  }

  public String getKind() {
    return kind;
  }

  /** The virtual loader id this loader merges into, or {@code null} to stand alone. */
  public String getGroup() {
    return group;
  }

  /**
   * @return true when {@code id} is a virtual loader id rather than an identity hash.
   */
  public static boolean isGroupId(String id) {
    return id != null && id.startsWith(GROUP_PREFIX);
  }

  /**
   * Classifies one loader. The webapp context is probed only when no class-name rule matched, which
   * keeps the reflective {@link ClassIndex#contextOf} probe off the thousands of generated and
   * reflection loaders that will never have one.
   */
  public static LoaderProfile of(ClassLoader loader) {
    if (loader == null) {
      return BOOTSTRAP;
    }
    Class<?> type = loader.getClass();
    LoaderProfile byName = BY_CLASS_NAME.get(type.getName());
    if (byName != null) {
      return byName;
    }
    return of(type.getName(), type.getSimpleName(), ClassIndex.contextOf(loader));
  }

  /**
   * The rule table itself, addressable by name — so it can be exercised without conjuring a loader
   * instance per rule, several of which live in packages a test has no business defining classes
   * in.
   */
  static LoaderProfile of(String className, String simpleName, String context) {
    LoaderProfile byName = BY_CLASS_NAME.get(className);
    if (byName != null) {
      return byName;
    }
    if (context != null) {
      return forContext(context);
    }
    return new LoaderProfile(
        simpleName == null || simpleName.isEmpty() ? className : simpleName, KIND_UNKNOWN, null);
  }

  private static LoaderProfile forContext(String context) {
    if (CONTEXT_TOOL.equals(context)) {
      return new LoaderProfile("telltale-diag WAR (this tool)", KIND_TOOL, null);
    }
    return new LoaderProfile("Application (" + context + ")", KIND_APP, null, context);
  }

  private static LoaderProfile webapp(String context) {
    return new LoaderProfile("Web application (" + context + ")", KIND_WEBAPP, null);
  }

  /**
   * This profile once the loader's classes are known. The context alone does not make a loader the
   * application: the lab cycle JVM carries a second {@code /worker} loader beside the real one
   * holding a single {@code com.sun} class. A loader in an application context with nothing but
   * platform classes is listed as a plain web application loader, so the application stays the one
   * loader a picker can default to.
   */
  LoaderProfile holding(Collection<String> classNames) {
    if (!KIND_APP.equals(kind)) {
      return this;
    }
    for (String className : classNames) {
      if (!ClassIndex.isPlatform(className)) {
        return this;
      }
    }
    return webapp(context);
  }

  /** Sort position for {@link #getKind()}; an unrecognised kind sorts as unknown. */
  public static int rank(String kind) {
    int index = KIND_ORDER.indexOf(kind);
    return index < 0 ? KIND_ORDER.indexOf(KIND_UNKNOWN) : index;
  }

  /**
   * A one-line summary of what a loader holds, derived from the class names it actually loaded
   * rather than hardcoded per loader — so it stays true as the deployment changes, and works on a
   * tier this table has never seen.
   *
   * <p>{@link #KIND_GENERATED} loaders are summarised by the family of the generated class ({@code
   * OnChangeProcessor}, {@code MathEngine}) because their package is a single constant and would
   * say nothing. Everything else is summarised by the libraries it holds.
   */
  public static String describe(String kind, Collection<String> classNames) {
    return join(KIND_GENERATED.equals(kind) ? topFamilies(classNames) : topLibraries(classNames));
  }

  private static List<String> topLibraries(Collection<String> classNames) {
    Map<String, Integer> counts = new LinkedHashMap<>();
    for (String name : classNames) {
      tally(counts, libraryOf(name));
    }
    return top(counts);
  }

  /** Known library name, else the first two package segments, else the name itself. */
  private static String libraryOf(String className) {
    for (Map.Entry<String, String> library : LIBRARY_NAMES.entrySet()) {
      String prefix = library.getKey();
      if (className.startsWith(prefix + ".")) {
        return library.getValue();
      }
    }
    int first = className.indexOf('.');
    if (first < 0) {
      return className;
    }
    int second = className.indexOf('.', first + 1);
    return second < 0 ? className.substring(0, first) : className.substring(0, second);
  }

  private static List<String> topFamilies(Collection<String> classNames) {
    Map<String, Integer> counts = new LinkedHashMap<>();
    for (String name : classNames) {
      tally(counts, familyOf(name));
    }
    return top(counts);
  }

  /**
   * The generated-class family: application names a compiled rule {@code
   * OnChangeProcessor_PolicyScreen_MandateStartDate_<md5>}, so the part before the first underscore
   * is the kind of rule it is.
   */
  private static String familyOf(String className) {
    int lastDot = className.lastIndexOf('.');
    String simple = lastDot < 0 ? className : className.substring(lastDot + 1);
    int underscore = simple.indexOf('_');
    return underscore < 0 ? simple : simple.substring(0, underscore);
  }

  private static void tally(Map<String, Integer> counts, String key) {
    Integer previous = counts.get(key);
    counts.put(key, previous == null ? 1 : previous + 1);
  }

  /** The most common keys, ties broken alphabetically so the output is stable. */
  private static List<String> top(Map<String, Integer> counts) {
    List<Map.Entry<String, Integer>> ordered = new ArrayList<>(counts.entrySet());
    ordered.sort(
        Comparator.comparingInt((Map.Entry<String, Integer> entry) -> entry.getValue())
            .reversed()
            .thenComparing(Map.Entry::getKey));
    Set<String> top = new LinkedHashSet<>();
    for (Map.Entry<String, Integer> entry : ordered) {
      if (top.size() == DESCRIPTION_FACETS) {
        break;
      }
      top.add(entry.getKey());
    }
    return new ArrayList<>(top);
  }

  private static String join(List<String> parts) {
    StringBuilder out = new StringBuilder();
    for (String part : parts) {
      if (out.length() > 0) {
        out.append(", ");
      }
      out.append(part);
    }
    return out.toString();
  }
}
