package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.File;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Map;
import org.junit.jupiter.api.Test;

/**
 * Exercises the real compiler against real source. The build JDK has a built-in javac, so these run
 * without the tools.jar fallback that a JRE-only application image needs — that path is covered by
 * {@link InstrumentationServiceTest}'s JDK discovery and by a live deploy.
 */
final class CompilerServiceTest {

  private static final String PATCHABLE =
      "package com.flanksource.telltale.diag.fixture;\n"
          + "public class Patchable {\n"
          + "    public String greet() { return \"patched\"; }\n"
          + "}\n";

  private final CompilerService compiler = new CompilerService();

  private static CompilerService.Request request(String className, String source) {
    // A null target means "no class to derive a classpath from"; the request
    // then compiles against java.class.path, which is all these fixtures need.
    return new CompilerService.Request(className, source, null);
  }

  @Test
  void compilesSourceToClassBytesNamedByTheirBinaryName() {
    CompilerService.Result result =
        compiler.compile(request("com.flanksource.telltale.diag.fixture.Patchable", PATCHABLE));

    assertTrue(result.isSuccess(), () -> "expected success, got " + result.getMessages());
    assertEquals(
        Arrays.asList("com.flanksource.telltale.diag.fixture.Patchable"),
        new ArrayList<>(result.getClasses().keySet()));

    byte[] bytes = result.getClasses().get("com.flanksource.telltale.diag.fixture.Patchable");
    assertTrue(bytes.length > 0);
    assertEquals(
        0xCAFEBABEL,
        ((long) (bytes[0] & 0xFF) << 24
                | (bytes[1] & 0xFF) << 16
                | (bytes[2] & 0xFF) << 8
                | (bytes[3] & 0xFF))
            & 0xFFFFFFFFL,
        "javac must emit a real class file");
  }

  /**
   * Redefining a class means redefining its nested classes with it, so the compiler has to hand
   * back every class file javac produced, not just the top-level one.
   */
  @Test
  void returnsNestedAndAnonymousClassesAlongsideTheTopLevelClass() {
    String source =
        "package com.flanksource.telltale.diag.fixture;\n"
            + "public class WithNested {\n"
            + "    static class Inner { }\n"
            + "    Runnable r = new Runnable() { public void run() { } };\n"
            + "}\n";

    CompilerService.Result result =
        compiler.compile(request("com.flanksource.telltale.diag.fixture.WithNested", source));

    assertTrue(result.isSuccess(), () -> String.valueOf(result.getMessages()));
    assertTrue(result.getClasses().containsKey("com.flanksource.telltale.diag.fixture.WithNested"));
    assertTrue(
        result.getClasses().containsKey("com.flanksource.telltale.diag.fixture.WithNested$Inner"));
    assertTrue(
        result.getClasses().containsKey("com.flanksource.telltale.diag.fixture.WithNested$1"),
        "the anonymous Runnable must come back too: " + result.getClasses().keySet());
  }

  @Test
  void reportsCompileErrorsWithTheirPositionInsteadOfThrowing() {
    String broken =
        "package com.flanksource.telltale.diag.fixture;\n"
            + "public class Broken {\n"
            + "    public String greet() { return missingSymbol; }\n"
            + "}\n";

    CompilerService.Result result =
        compiler.compile(request("com.flanksource.telltale.diag.fixture.Broken", broken));

    assertFalse(result.isSuccess());
    assertTrue(result.getClasses().isEmpty(), "a failed compile must produce no bytes to apply");
    CompilerService.Message error =
        result.getMessages().stream()
            .filter(CompilerService.Message::isError)
            .findFirst()
            .orElseThrow(() -> new AssertionError("no error reported: " + result.getMessages()));
    assertEquals(
        3L, error.toMap().get("line"), "the error must point at the line the caller edited");
    assertTrue(String.valueOf(error.toMap().get("message")).contains("missingSymbol"));
  }

  /** javac itself catches a public type whose name does not match the request. */
  @Test
  void failsWhenTheSourceDeclaresADifferentPublicTypeThanTheClassBeingPatched() {
    CompilerService.Result result =
        compiler.compile(request("com.flanksource.telltale.diag.fixture.Expected", PATCHABLE));

    assertFalse(result.isSuccess());
    assertTrue(
        result.getMessages().stream()
            .anyMatch(
                m ->
                    String.valueOf(m.toMap().get("message"))
                        .contains("should be declared in a file named")),
        () -> String.valueOf(result.getMessages()));
  }

  /**
   * A package-private type may legally carry any name, so javac compiles this happily — and
   * applying it would redefine nothing the caller asked about. That silent no-op is the case this
   * check exists to turn into a failure.
   */
  @Test
  void failsWhenCompilationProducesNoClassWithTheRequestedName() {
    String misnamed =
        "package com.flanksource.telltale.diag.fixture;\n"
            + "class SomethingElse {\n"
            + "    String greet() { return \"patched\"; }\n"
            + "}\n";

    CompilerService.Result result =
        compiler.compile(request("com.flanksource.telltale.diag.fixture.Requested", misnamed));

    assertFalse(result.isSuccess(), "javac succeeds here; the service must not");
    assertTrue(
        result.getMessages().stream()
            .anyMatch(
                m ->
                    "telltale.diag.wrong-class".equals(m.toMap().get("code"))
                        && String.valueOf(m.toMap().get("message"))
                            .contains("must declare that exact type")),
        () -> String.valueOf(result.getMessages()));
  }

  @Test
  void rejectsAnEmptyClassNameOrSource() {
    assertThrows(IllegalArgumentException.class, () -> request("", PATCHABLE));
    assertThrows(IllegalArgumentException.class, () -> request("com.X", "  "));
  }

  @Test
  void reportsHowManyClasspathEntriesWereUsedSoAMissingOneIsDiagnosable() {
    Map<String, Object> summary =
        compiler
            .compile(request("com.flanksource.telltale.diag.fixture.Patchable", PATCHABLE))
            .toMap();

    assertTrue((Integer) summary.get("classpathEntries") > 0);
    assertEquals(true, summary.get("success"));
  }

  @Test
  void countsClasspathEntriesBySeparator() {
    assertEquals(0, CompilerService.entryCount(""));
    assertEquals(1, CompilerService.entryCount("/a.jar"));
    assertEquals(
        3, CompilerService.entryCount(String.join(File.pathSeparator, "/a.jar", "/b", "/c.jar")));
  }

  /**
   * An exploded webapp puts the patched class in WEB-INF/classes and everything it was compiled
   * against in the sibling WEB-INF/lib — the whole reason the classpath is derived from the code
   * source rather than from the classloader, whose getURLs() TomEE does not usefully implement.
   */
  @Test
  void derivesTheClasspathFromTheCodeSourceIncludingSiblingJars() {
    String classpath = CompilerService.classpathFor(CompilerServiceTest.class);

    assertTrue(CompilerService.entryCount(classpath) > 0);
    assertTrue(classpath.contains(File.separator), "entries must be real paths: " + classpath);
  }

  @Test
  void derivesAnEmptyButUsableClasspathForAClassWithNoCodeSource() {
    // java.lang.String is bootstrap-loaded: no code source, no classloader.
    String classpath = CompilerService.classpathFor(String.class);

    assertFalse(
        classpath.contains("null"), "a missing code source must contribute nothing: " + classpath);
  }
}
