package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.Arrays;
import java.util.Collections;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;

/** The loader taxonomy: what a loader is called, how it sorts, and what it merges into. */
final class LoaderProfileTest {

  @ParameterizedTest(name = "{0} in context {1} is \"{2}\" ({3})")
  @CsvSource({
    "org.codehaus.janino.ByteArrayClassLoader,,Runtime compiled classes,generated,~compiled",
    "sun.reflect.DelegatingClassLoader,,JDK reflection accessors,jdk,~jdk-reflection",
    "javax.management.remote.rmi.NoCallStackClassLoader,,JMX RMI connector,jdk,~jmx-rmi",
    "io.opentelemetry.javaagent.bootstrap.AgentClassLoader,,OpenTelemetry agent,agent,",
    "sun.misc.Launcher$AppClassLoader,,JVM classpath + java agents,container,",
    "sun.misc.Launcher$ExtClassLoader,,JDK extensions (lib/ext),jdk,",
    "sun.reflect.misc.MethodUtil,,JDK introspection trampoline,jdk,",
    "org.apache.tomee.catalina.TomEEWebappClassLoader,/application,Application (/application),app,",
    "org.apache.tomee.catalina.TomEEWebappClassLoader,/worker,Application (/worker),app,",
    "org.apache.tomee.catalina.TomEEWebappClassLoader,/telltale-diag,telltale-diag WAR (this"
        + " tool),tool,",
    "org.apache.tomee.catalina.TomEEWebappClassLoader,/manager,Application (/manager),app,",
  })
  void classifiesALoaderByItsClassNameThenItsWebappContext(
      String className, String context, String label, String kind, String group) {
    LoaderProfile profile = LoaderProfile.of(className, simpleNameOf(className), context);

    assertEquals(label, profile.getLabel());
    assertEquals(kind, profile.getKind());
    assertEquals(group, profile.getGroup());
  }

  /**
   * The lab cycle JVM carries a second loader in the /worker context holding one com.sun class
   * beside the real one. A picker defaults to the application loader only when there is exactly
   * one, so a loader with nothing but platform classes must not claim to be the application.
   */
  @ParameterizedTest(name = "{0} holding {1} is \"{2}\" ({3})")
  @CsvSource({
    "/worker,com.sun.xml.bind.v2.ContextFactory,Web application (/worker),webapp",
    "/application,java.lang.Object;javax.servlet.Servlet,Web application (/application),webapp",
    "/worker,com.sun.xml.bind.v2.ContextFactory;example.application.pas.bll.ActivityBll,Application"
        + " (/worker),app",
    "/manager,com.sun.xml.bind.v2.ContextFactory,Web application (/manager),webapp",
  })
  void keepsAnApplicationContextLoaderAsTheApplicationOnlyWhileItHoldsApplicationClasses(
      String context, String classes, String label, String kind) {
    LoaderProfile profile =
        LoaderProfile.of(
                "org.apache.tomee.catalina.TomEEWebappClassLoader",
                "TomEEWebappClassLoader",
                context)
            .holding(Arrays.asList(classes.split(";")));

    assertEquals(label, profile.getLabel());
    assertEquals(kind, profile.getKind());
  }

  /**
   * A class-name rule wins over the context, so the two noisy families are recognised without
   * paying for the reflective context probe.
   */
  @Test
  void prefersTheClassNameRuleOverTheContext() {
    LoaderProfile profile =
        LoaderProfile.of(
            "org.codehaus.janino.ByteArrayClassLoader", "ByteArrayClassLoader", "/application");

    assertEquals(LoaderProfile.GROUP_COMPILED, profile.getGroup());
  }

  /**
   * java.net.URLClassLoader is deliberately absent from the rule table. On the lab JVM it is
   * TomEE's shared lib/ loader, but that is a fact about one deployment rather than about the
   * class, so it takes the fallback and lets its contents describe it instead of carrying a label
   * that is a guess.
   */
  @Test
  void leavesAPlainUrlClassLoaderUnclassifiedRatherThanGuessingWhoseItIs() {
    LoaderProfile profile = LoaderProfile.of("java.net.URLClassLoader", "URLClassLoader", null);

    assertEquals("URLClassLoader", profile.getLabel());
    assertEquals(LoaderProfile.KIND_UNKNOWN, profile.getKind());
    assertNull(profile.getGroup());
  }

  @Test
  void fallsBackToTheFullNameWhenTheLoaderIsAnonymous() {
    LoaderProfile profile = LoaderProfile.of("com.example.Outer$1", "", null);

    assertEquals("com.example.Outer$1", profile.getLabel());
  }

  @Test
  void namesTheBootstrapLoader() {
    assertEquals(ClassIndex.BOOTSTRAP_LABEL, LoaderProfile.of(null).getLabel());
    assertEquals(LoaderProfile.KIND_JDK, LoaderProfile.of(null).getKind());
    assertNull(LoaderProfile.of(null).getGroup());
  }

  @Test
  void tellsAVirtualIdApartFromAnIdentityHash() {
    assertTrue(LoaderProfile.isGroupId(LoaderProfile.GROUP_COMPILED));
    assertFalse(LoaderProfile.isGroupId("188b9a1e"));
    assertFalse(LoaderProfile.isGroupId(ClassIndex.BOOTSTRAP_HASH));
    assertFalse(LoaderProfile.isGroupId(null));
  }

  /** Application code has to sort above platform noise, or the merge buys nothing. */
  @Test
  void ranksApplicationCodeAheadOfPlatformNoise() {
    assertTrue(
        LoaderProfile.rank(LoaderProfile.KIND_APP)
            < LoaderProfile.rank(LoaderProfile.KIND_CONTAINER));
    assertTrue(
        LoaderProfile.rank(LoaderProfile.KIND_CONTAINER)
            < LoaderProfile.rank(LoaderProfile.KIND_GENERATED));
    assertTrue(
        LoaderProfile.rank(LoaderProfile.KIND_GENERATED)
            < LoaderProfile.rank(LoaderProfile.KIND_JDK));
    assertEquals(
        LoaderProfile.rank(LoaderProfile.KIND_UNKNOWN), LoaderProfile.rank("no such kind"));
  }

  /**
   * A loader nothing matched is not JDK noise — every JDK loader worth naming is in the table.
   * Ranking unknown below jdk would bury TomEE's shared lib/ loader, the biggest unclassified one
   * in the JVM, beneath the bootstrap loader and the reflection accessors.
   */
  @Test
  void ranksAnUnclassifiedLoaderAheadOfKnownJdkNoise() {
    assertTrue(
        LoaderProfile.rank(LoaderProfile.KIND_UNKNOWN)
            < LoaderProfile.rank(LoaderProfile.KIND_JDK));
  }

  @Test
  void describesALoaderByTheLibrariesItHolds() {
    List<String> classes =
        Arrays.asList(
            "org.eclipse.persistence.jpa.JpaEntityManager",
            "org.eclipse.persistence.sessions.Session",
            "com.tangosol.net.CacheFactory",
            "com.tangosol.util.Binary",
            "org.apache.openejb.OpenEJB",
            "com.microsoft.sqlserver.jdbc.SQLServerDriver");

    assertEquals(
        "Coherence, EclipseLink, MSSQL JDBC, OpenEJB",
        LoaderProfile.describe(LoaderProfile.KIND_CONTAINER, classes));
  }

  /** An unmapped package degrades to its first two segments, never to nothing. */
  @Test
  void fallsBackToThePackagePrefixForAnUnknownLibrary() {
    assertEquals(
        "com.example",
        LoaderProfile.describe(
            LoaderProfile.KIND_CONTAINER, Arrays.asList("com.example.deep.nested.Thing")));
    assertEquals(
        "Unpackaged",
        LoaderProfile.describe(
            LoaderProfile.KIND_CONTAINER, Collections.singletonList("Unpackaged")));
  }

  /**
   * The generated loaders all share one package, so naming it would say nothing. What distinguishes
   * them is the kind of rule application compiled, which is the part of the name before the first
   * underscore.
   */
  @Test
  void describesGeneratedClassesByRuleFamilyRatherThanByPackage() {
    List<String> classes =
        Arrays.asList(
            "example.application.sre.bll.generatedClasses.OnChangeProcessor_PolicyScreen_PayDay_a1b2",
            "example.application.sre.bll.generatedClasses.OnChangeProcessor_PolicyScreen_PlanCode_c3d4",
            "example.application.sre.bll.generatedClasses.MathEngine",
            "example.application.sre.bll.generatedClasses.OnLoadProcessor_PolicyScreen_e5f6");

    assertEquals(
        "OnChangeProcessor, MathEngine, OnLoadProcessor",
        LoaderProfile.describe(LoaderProfile.KIND_GENERATED, classes));
  }

  /** Ties break alphabetically so two runs of the same JVM describe it the same way. */
  @Test
  void capsTheDescriptionAndBreaksTiesAlphabetically() {
    List<String> classes =
        Arrays.asList(
            "org.slf4j.Logger",
            "net.sf.saxon.Configuration",
            "org.aspectj.weaver.World",
            "okhttp3.Call",
            "org.objectweb.asm.ClassReader");

    assertEquals(
        "ASM, AspectJ, OkHttp, SLF4J",
        LoaderProfile.describe(LoaderProfile.KIND_CONTAINER, classes));
  }

  private static String simpleNameOf(String className) {
    int lastDot = className.lastIndexOf('.');
    return lastDot < 0 ? className : className.substring(lastDot + 1);
  }
}
