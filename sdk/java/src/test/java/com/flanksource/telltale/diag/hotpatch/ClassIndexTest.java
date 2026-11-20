package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Map;
import java.util.NoSuchElementException;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;

/**
 * Contract §1/§2 over a synthetic class list: no live Instrumentation, so the grouping, search,
 * sort, and pagination rules are exercised on their own.
 */
final class ClassIndexTest {

  /** Stands in for Tomcat's WebappClassLoaderBase, which exposes the context this way. */
  private static final class WebappLoader extends ClassLoader {
    private final String contextName;

    WebappLoader(String contextName) {
      super(null);
      this.contextName = contextName;
    }

    @SuppressWarnings("unused") // read reflectively by ClassIndex.label
    public String getContextName() {
      return contextName;
    }
  }

  private static final ClassLoader PAS = new WebappLoader("/application");
  private static final ClassLoader CYCLE = new WebappLoader("/worker");
  private static final ClassLoader PLAIN = new ClassLoader(null) {};

  private static final List<String> PAS_CLASSES =
      Arrays.asList(
          "example.application.pas.dal.ActivityDal",
          "example.application.pas.dal.PolicyDal",
          "example.application.pas.bll.ActivityBll",
          "example.vendor.cycle.CycleServiceBll");
  private static final List<String> CYCLE_CLASSES =
      Arrays.asList("example.vendor.cycle.CycleRunner", "example.vendor.cycle.CycleTask");
  private static final List<String> BOOTSTRAP_CLASSES = Arrays.asList("java.lang.String");

  private static ClassIndex index() {
    List<ClassIndex.Entry> entries = new ArrayList<>();
    for (String name : PAS_CLASSES) {
      entries.add(new ClassIndex.Entry(name, PAS));
    }
    for (String name : CYCLE_CLASSES) {
      entries.add(new ClassIndex.Entry(name, CYCLE));
    }
    for (String name : BOOTSTRAP_CLASSES) {
      entries.add(new ClassIndex.Entry(name, null));
    }
    return new ClassIndex(entries);
  }

  @SuppressWarnings("unchecked")
  private static List<Map<String, Object>> classesOf(Map<String, Object> page) {
    return (List<Map<String, Object>>) page.get("classes");
  }

  private static List<String> namesOf(Map<String, Object> page) {
    List<String> names = new ArrayList<>();
    for (Map<String, Object> row : classesOf(page)) {
      names.add(String.valueOf(row.get("name")));
    }
    return names;
  }

  @Test
  void reportsBootstrapAsHashZeroWithAnEmptyClassName() {
    assertEquals(ClassIndex.BOOTSTRAP_HASH, ClassIndex.hash(null));
    assertEquals(ClassIndex.BOOTSTRAP_LABEL, ClassIndex.label(null));

    Map<String, Object> bootstrap =
        index().loaders().stream()
            .filter(row -> ClassIndex.BOOTSTRAP_HASH.equals(row.get("hash")))
            .findFirst()
            .orElseThrow(() -> new AssertionError("no bootstrap row"));
    assertEquals("", bootstrap.get("className"));
    assertEquals(BOOTSTRAP_CLASSES.size(), bootstrap.get("classCount"));
  }

  @Test
  void hashesANonBootstrapLoaderByItsIdentityHashCode() {
    assertEquals(Integer.toHexString(System.identityHashCode(PAS)), ClassIndex.hash(PAS));
  }

  @Test
  void labelsAWebappLoaderFromItsContextPathAndOthersFromTheirSimpleClassName() {
    assertEquals("Application (/application)", ClassIndex.label(PAS));
    assertEquals("Application (/worker)", ClassIndex.label(CYCLE));
    assertEquals(
        PLAIN.getClass().getSimpleName().isEmpty()
            ? PLAIN.getClass().getName()
            : PLAIN.getClass().getSimpleName(),
        ClassIndex.label(PLAIN));
  }

  @Test
  void ordersLoadersByClassCountDescending() {
    List<Map<String, Object>> loaders = index().loaders();

    assertEquals(
        Arrays.asList(ClassIndex.hash(PAS), ClassIndex.hash(CYCLE), ClassIndex.BOOTSTRAP_HASH),
        Arrays.asList(
            loaders.get(0).get("hash"), loaders.get(1).get("hash"), loaders.get(2).get("hash")));
    assertEquals(
        Arrays.asList(PAS_CLASSES.size(), CYCLE_CLASSES.size(), BOOTSTRAP_CLASSES.size()),
        Arrays.asList(
            loaders.get(0).get("classCount"),
            loaders.get(1).get("classCount"),
            loaders.get(2).get("classCount")));
  }

  @Test
  void listsEveryClassSortedByNameWhenPlatformClassesAreIncluded() {
    Map<String, Object> page = index().page(null, null, ClassIndex.DEFAULT_LIMIT, 0, true);

    int total = PAS_CLASSES.size() + CYCLE_CLASSES.size() + BOOTSTRAP_CLASSES.size();
    assertEquals(total, page.get("total"));
    assertEquals(0, page.get("platformExcluded"));
    assertEquals(
        Arrays.asList(
            "example.application.pas.bll.ActivityBll",
            "example.application.pas.dal.ActivityDal",
            "example.application.pas.dal.PolicyDal",
            "example.vendor.cycle.CycleRunner",
            "example.vendor.cycle.CycleServiceBll",
            "example.vendor.cycle.CycleTask",
            "java.lang.String"),
        namesOf(page));
  }

  /**
   * The JDK and the EE APIs are the bulk of getAllLoadedClasses() and drown the application
   * classes, so the listing hides them by default — but says how many it hid, so the omission is
   * visible rather than silent.
   */
  @Test
  void hidesPlatformClassesByDefaultAndReportsHowManyItHid() {
    Map<String, Object> page = index().page(null, null, ClassIndex.DEFAULT_LIMIT, 0, false);

    assertEquals(PAS_CLASSES.size() + CYCLE_CLASSES.size(), page.get("total"));
    assertEquals(BOOTSTRAP_CLASSES.size(), page.get("platformExcluded"));
    assertFalse(namesOf(page).contains("java.lang.String"));
  }

  /**
   * Array and primitive classes come back from getAllLoadedClasses() under JVM descriptor names,
   * which the package tree splits on '.' into junk nodes ("[[Lcom", "[[[Lcom"). They have no class
   * file to decompile either.
   */
  @Test
  void keepsArrayAndPrimitiveClassesOutOfTheIndex() {
    assertTrue(
        String[].class.getName().startsWith("[L"),
        "the premise: an array class reports a descriptor name, not a source name");

    assertFalse(ClassIndex.isBrowsable(String[].class));
    assertFalse(ClassIndex.isBrowsable(int[][].class));
    assertFalse(ClassIndex.isBrowsable(int.class));
    assertFalse(ClassIndex.isBrowsable(void.class));
    assertTrue(ClassIndex.isBrowsable(String.class));
    assertTrue(ClassIndex.isBrowsable(ClassIndex.class));
  }

  @ParameterizedTest(name = "{0} is platform={1}")
  @CsvSource({
    "java.lang.String, true",
    "javax.servlet.http.HttpServlet, true",
    "javax.persistence.EntityManager, true",
    "jakarta.servlet.Filter, true",
    "sun.misc.Unsafe, true",
    "com.sun.crypto.provider.AESCipher, true",
    "jdk.internal.misc.VM, true",
    "org.w3c.dom.Node, true",
    "org.xml.sax.XMLReader, true",
    "example.application.pas.bll.ActivityBll, false",
    "example.vendor.cycle.CycleRunner, false",
    "org.apache.tomee.catalina.TomEEWebappClassLoader, false",
    "org.springframework.beans.factory.BeanFactory, false",
    "javaxx.NotPlatform, false",
    "javassist.ClassPool, false",
  })
  void classifiesPlatformPackagesByPrefix(String className, boolean platform) {
    assertEquals(platform, ClassIndex.isPlatform(className));
  }

  @Test
  void restrictsResultsToOneLoader() {
    Map<String, Object> page =
        index().page(ClassIndex.hash(CYCLE), null, ClassIndex.DEFAULT_LIMIT, 0, false);

    assertEquals(CYCLE_CLASSES.size(), page.get("total"));
    assertEquals(
        Arrays.asList("example.vendor.cycle.CycleRunner", "example.vendor.cycle.CycleTask"),
        namesOf(page));
    for (Map<String, Object> row : classesOf(page)) {
      assertEquals(ClassIndex.hash(CYCLE), row.get("loader"));
    }
  }

  @ParameterizedTest(name = "q={0} matches {1} classes")
  @CsvSource({
    "ActivityDal, 1",
    "activitydal, 1",
    "ACTIVITY, 2",
    "example.vendor.cycle, 3",
    "NoSuchClass, 0",
  })
  void matchesTheQueryCaseInsensitivelyAnywhereInTheName(String query, int expected) {
    assertEquals(
        expected, index().page(null, query, ClassIndex.DEFAULT_LIMIT, 0, false).get("total"));
  }

  @Test
  void countsEveryMatchButReturnsOnlyTheRequestedWindow() {
    int limit = 2;
    int offset = 1;
    Map<String, Object> page = index().page(ClassIndex.hash(PAS), null, limit, offset, false);

    assertEquals(PAS_CLASSES.size(), page.get("total"));
    assertEquals(offset, page.get("offset"));
    assertEquals(limit, page.get("limit"));
    assertEquals(
        Arrays.asList(
            "example.application.pas.dal.ActivityDal", "example.application.pas.dal.PolicyDal"),
        namesOf(page));
  }

  @Test
  void returnsAnEmptyPageWhenTheOffsetIsPastTheEnd() {
    Map<String, Object> page = index().page(null, null, ClassIndex.DEFAULT_LIMIT, 500, true);

    assertEquals(
        PAS_CLASSES.size() + CYCLE_CLASSES.size() + BOOTSTRAP_CLASSES.size(), page.get("total"));
    assertTrue(classesOf(page).isEmpty());
  }

  @ParameterizedTest(name = "limit={0} -> {1}")
  @CsvSource({
    ", 200",
    "'', 200",
    "1, 1",
    "1000, 1000",
  })
  void defaultsTheLimitTo200AndAcceptsUpTo1000(String raw, int expected) {
    assertEquals(expected, ClassIndex.parseLimit(raw));
  }

  @ParameterizedTest(name = "limit={0} is rejected")
  @CsvSource({"0", "-1", "1001", "many"})
  void rejectsAnOutOfRangeOrNonNumericLimit(String raw) {
    assertThrows(IllegalArgumentException.class, () -> ClassIndex.parseLimit(raw));
  }

  @ParameterizedTest(name = "offset={0} -> {1}")
  @CsvSource({", 0", "'', 0", "0, 0", "25, 25"})
  void defaultsTheOffsetToZero(String raw, int expected) {
    assertEquals(expected, ClassIndex.parseOffset(raw));
  }

  @ParameterizedTest(name = "offset={0} is rejected")
  @CsvSource({"-1", "later"})
  void rejectsANegativeOrNonNumericOffset(String raw) {
    assertThrows(IllegalArgumentException.class, () -> ClassIndex.parseOffset(raw));
  }

  @Test
  void knowsWhichLoaderHashesExist() {
    ClassIndex index = index();

    assertTrue(index.hasLoader(ClassIndex.hash(PAS)));
    assertTrue(index.hasLoader(ClassIndex.BOOTSTRAP_HASH));
    assertFalse(index.hasLoader(ClassIndex.hash(PLAIN)));
  }

  @Test
  void resolvesADuplicateNameToTheLoaderThatHasLoadedTheMostClasses() {
    String duplicated = "example.application.shared.Duplicated";
    List<ClassIndex.Entry> entries = new ArrayList<>();
    for (String name : PAS_CLASSES) {
      entries.add(new ClassIndex.Entry(name, PAS));
    }
    entries.add(new ClassIndex.Entry(duplicated, CYCLE));
    entries.add(new ClassIndex.Entry(duplicated, PAS));

    ClassIndex index = new ClassIndex(entries);
    assertEquals(ClassIndex.hash(PAS), index.resolve(duplicated, null).getLoaderHash());
    assertEquals(
        ClassIndex.hash(CYCLE), index.resolve(duplicated, ClassIndex.hash(CYCLE)).getLoaderHash());
  }

  @Test
  void failsLoudlyWhenTheClassIsNotLoadedAtAllOrNotByTheGivenLoader() {
    ClassIndex index = index();

    assertThrows(NoSuchElementException.class, () -> index.resolve("com.example.Missing", null));
    assertThrows(
        NoSuchElementException.class,
        () -> index.resolve("example.vendor.cycle.CycleRunner", ClassIndex.hash(PAS)));
  }

  // --- merged (virtual) loaders -------------------------------------------

  private static final ClassLoader RULE_A = new org.codehaus.janino.ByteArrayClassLoader();
  private static final ClassLoader RULE_B = new org.codehaus.janino.ByteArrayClassLoader();

  private static final String RULE_A_CLASS =
      "example.application.sre.bll.generatedClasses.OnChangeProcessor_PolicyScreen_PayDay_a1b2";
  private static final String RULE_B_CLASS =
      "example.application.sre.bll.generatedClasses.MathEngine";

  /** PAS plus two single-rule janino loaders, the shape the live JVM has 349 of. */
  private static ClassIndex indexWithCompiledRules() {
    List<ClassIndex.Entry> entries = new ArrayList<>();
    for (String name : PAS_CLASSES) {
      entries.add(new ClassIndex.Entry(name, PAS));
    }
    entries.add(new ClassIndex.Entry(RULE_A_CLASS, RULE_A));
    entries.add(new ClassIndex.Entry(RULE_A_CLASS + "$LocalContext", RULE_A));
    entries.add(new ClassIndex.Entry(RULE_B_CLASS, RULE_B));
    return new ClassIndex(entries);
  }

  private static Map<String, Object> loaderRow(ClassIndex index, String hash) {
    return index.loaders().stream()
        .filter(row -> hash.equals(row.get("hash")))
        .findFirst()
        .orElseThrow(() -> new AssertionError("no row for loader " + hash));
  }

  /**
   * A picker with one row per loader is unusable when a JVM carries hundreds of single-rule
   * loaders, so the family collapses to one row that reports the totals across its members.
   */
  @Test
  void mergesTheCompiledRuleLoadersIntoOneVirtualRow() {
    ClassIndex index = indexWithCompiledRules();

    List<String> hashes = new ArrayList<>();
    for (Map<String, Object> row : index.loaders()) {
      hashes.add(String.valueOf(row.get("hash")));
    }
    assertEquals(Arrays.asList(ClassIndex.hash(PAS), LoaderProfile.GROUP_COMPILED), hashes);

    Map<String, Object> merged = loaderRow(index, LoaderProfile.GROUP_COMPILED);
    assertEquals(3, merged.get("classCount"));
    assertEquals(2, merged.get("loaderCount"));
    assertEquals(Boolean.TRUE, merged.get("virtual"));
    assertEquals("Runtime compiled classes (2 loaders)", merged.get("label"));
    assertEquals("OnChangeProcessor, MathEngine", merged.get("description"));
  }

  /** Every row says what it is and what it holds, not just which class implements it. */
  @Test
  void describesEachLoaderByWhatItHolds() {
    Map<String, Object> pas = loaderRow(indexWithCompiledRules(), ClassIndex.hash(PAS));

    assertEquals("Application (/application)", pas.get("label"));
    assertEquals(LoaderProfile.KIND_APP, pas.get("kind"));
    assertEquals("example.application, example.vendor", pas.get("description"));
  }

  /**
   * The shape of the lab cycle JVM: the real /worker loader plus a sibling in the same context
   * holding one platform class. Only the real one is listed as the application, so the web UI has
   * exactly one to default to.
   */
  @Test
  void listsOneApplicationLoaderWhenAnApplicationContextSiblingHoldsOnlyPlatformClasses() {
    ClassLoader platformOnly = new WebappLoader("/worker");
    List<ClassIndex.Entry> entries = new ArrayList<>();
    for (String name : CYCLE_CLASSES) {
      entries.add(new ClassIndex.Entry(name, CYCLE));
    }
    entries.add(new ClassIndex.Entry("com.sun.xml.bind.v2.ContextFactory", platformOnly));

    List<String> applications = new ArrayList<>();
    for (Map<String, Object> row : new ClassIndex(entries).loaders()) {
      if (LoaderProfile.KIND_APP.equals(row.get("kind"))) {
        applications.add(String.valueOf(row.get("hash")));
      }
    }

    assertEquals(Arrays.asList(ClassIndex.hash(CYCLE)), applications);
    assertEquals(
        "Web application (/worker)",
        loaderRow(new ClassIndex(entries), ClassIndex.hash(platformOnly)).get("label"));
  }

  /** Application code sorts above platform noise regardless of class counts. */
  @Test
  void ordersApplicationLoadersAheadOfBiggerPlatformOnes() {
    List<ClassIndex.Entry> entries = new ArrayList<>();
    entries.add(new ClassIndex.Entry("example.application.pas.bll.ActivityBll", PAS));
    for (int i = 0; i < 5; i++) {
      entries.add(new ClassIndex.Entry("java.lang.Type" + i, null));
    }

    List<Map<String, Object>> loaders = new ClassIndex(entries).loaders();

    assertEquals(ClassIndex.hash(PAS), loaders.get(0).get("hash"));
    assertEquals(ClassIndex.BOOTSTRAP_HASH, loaders.get(1).get("hash"));
  }

  /**
   * ClassesServlet 404s any loader it cannot find, so a virtual id that is not recognised here
   * would make every request naming one fail.
   */
  @Test
  void recognisesAVirtualIdAsAnExistingLoader() {
    ClassIndex index = indexWithCompiledRules();

    assertTrue(index.hasLoader(LoaderProfile.GROUP_COMPILED));
    assertFalse(index.hasLoader(LoaderProfile.GROUP_JDK_REFLECTION));
    assertFalse(index.hasLoader("~nonsense"));
  }

  /**
   * The merged id filters the listing, but each class still reports the real loader that defined it
   * — that is what keeps decompile and patch working from a merged view.
   */
  @Test
  void listsMergedClassesUnderTheirRealLoaderHash() {
    Map<String, Object> page =
        indexWithCompiledRules()
            .page(LoaderProfile.GROUP_COMPILED, null, ClassIndex.DEFAULT_LIMIT, 0, false);

    assertEquals(3, page.get("total"));
    List<String> loaders = new ArrayList<>();
    for (Map<String, Object> row : classesOf(page)) {
      loaders.add(String.valueOf(row.get("loader")));
    }
    assertEquals(
        Arrays.asList(ClassIndex.hash(RULE_B), ClassIndex.hash(RULE_A), ClassIndex.hash(RULE_A)),
        loaders);
    assertFalse(loaders.contains(LoaderProfile.GROUP_COMPILED));
  }

  @Test
  void resolvesThroughAVirtualIdToTheRealDefiningLoader() {
    ClassIndex.Entry entry =
        indexWithCompiledRules().resolve(RULE_A_CLASS, LoaderProfile.GROUP_COMPILED);

    assertEquals(ClassIndex.hash(RULE_A), entry.getLoaderHash());
  }

  /**
   * "Biggest loader wins" is a fair guess for a real hash. Applied to a merged id it would silently
   * pick one of hundreds of loaders the caller never named, so an ambiguous match refuses and lists
   * the real ones instead.
   */
  @Test
  void refusesToGuessWhichMemberOfAMergedIdWasMeant() {
    String shared = "example.application.sre.bll.generatedClasses.Shared";
    List<ClassIndex.Entry> entries =
        new ArrayList<>(
            Arrays.asList(
                new ClassIndex.Entry(shared, RULE_A), new ClassIndex.Entry(shared, RULE_B)));

    IllegalArgumentException failure =
        assertThrows(
            IllegalArgumentException.class,
            () -> new ClassIndex(entries).resolve(shared, LoaderProfile.GROUP_COMPILED));

    assertTrue(failure.getMessage().contains(ClassIndex.hash(RULE_A)), failure.getMessage());
    assertTrue(failure.getMessage().contains(ClassIndex.hash(RULE_B)), failure.getMessage());
  }

  /**
   * Redefining a class "in" a merged view is ambiguous by construction, even when only one member
   * currently defines the name: the id stands for the family, not for the loader that happens to
   * hold it today.
   */
  @Test
  void refusesAVirtualIdOutrightWhenResolvingSomethingAboutToBeModified() {
    ClassIndex index = indexWithCompiledRules();

    IllegalArgumentException failure =
        assertThrows(
            IllegalArgumentException.class,
            () -> index.resolveForWrite(RULE_A_CLASS, LoaderProfile.GROUP_COMPILED));
    assertTrue(failure.getMessage().contains(ClassIndex.hash(RULE_A)), failure.getMessage());

    assertEquals(
        ClassIndex.hash(RULE_A),
        index.resolveForWrite(RULE_A_CLASS, ClassIndex.hash(RULE_A)).getLoaderHash());
    assertEquals(
        ClassIndex.hash(RULE_A), index.resolveForWrite(RULE_A_CLASS, null).getLoaderHash());
  }

  @Test
  void collectsOnlyRealInnerClassesOfTheSameLoader() {
    String outer = "example.application.pas.bll.ActivityBll";
    List<ClassIndex.Entry> entries =
        new ArrayList<>(
            Arrays.asList(
                new ClassIndex.Entry(outer, PAS),
                new ClassIndex.Entry(outer + "$Row", PAS),
                new ClassIndex.Entry(outer + "$1", PAS),
                new ClassIndex.Entry(outer + "$$Lambda$17/0x1234", PAS),
                new ClassIndex.Entry(outer + "$$EnhancerBySpringCGLIB$$a1b2c3", PAS),
                new ClassIndex.Entry(outer + "$Row", CYCLE),
                new ClassIndex.Entry(outer + "Extra", PAS)));

    List<ClassIndex.Entry> inner = new ClassIndex(entries).innerClasses(entries.get(0));

    List<String> names = new ArrayList<>();
    for (ClassIndex.Entry entry : inner) {
      names.add(entry.getName());
    }
    assertEquals(Arrays.asList(outer + "$1", outer + "$Row"), names);
  }
}
