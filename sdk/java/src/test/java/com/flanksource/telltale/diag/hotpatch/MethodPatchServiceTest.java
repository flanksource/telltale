package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.fixture.ProbeFixture;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

/**
 * The whole replace-one-method path, end to end: generate a stand-in for the live class, compile
 * the author's method against it, splice that method's bytecode into the live bytes, then define
 * and run the result.
 *
 * <p>Only {@code Instrumentation.redefineClasses} is missing from what the pod does, and it is
 * missing on purpose — defining the spliced bytes in a classloader runs the same verifier the JVM
 * would, without needing an attached agent to run the tests.
 */
final class MethodPatchServiceTest {

  private static final String CLASS_NAME = ProbeFixture.class.getName();

  private final MethodPatchService service = new MethodPatchService();

  private MethodPatchService.Result compile(String selector, String source) {
    return service.compile(
        CLASS_NAME,
        ProbeFixture.class,
        LiveClass.bytecodeOf(ProbeFixture.class),
        MethodSelector.parse(selector),
        source,
        null);
  }

  private Class<?> patched(MethodPatchService.Result result) throws ClassNotFoundException {
    return LiveClass.define(
        CLASS_NAME, service.splice(CLASS_NAME, result, ProbeFixture.class.getClassLoader()));
  }

  @Test
  void replacesOneMethodAndLeavesEveryOtherRunningTheBytesItHad() throws Exception {
    MethodPatchService.Result result =
        compile(
            "greet(String)",
            "public static String greet(String name) { return \"howdy \" + name; }");

    assertTrue(result.isSuccess(), () -> String.valueOf(result.toMap().get("diagnostics")));
    assertEquals("(Ljava/lang/String;)Ljava/lang/String;", result.getDescriptor());

    Class<?> type = patched(result);
    assertEquals("howdy world", type.getMethod("greet", String.class).invoke(null, "world"));

    Object fixture = type.getConstructor(String.class).newInstance("p-");
    assertEquals(5, type.getMethod("add", int.class, int.class).invoke(fixture, 2, 3));
    assertEquals(
        "p-x",
        type.getMethod("prefixed", String.class).invoke(fixture, "x"),
        "a method nobody replaced must behave exactly as it did");
  }

  /**
   * javac reports against the file it compiled, and the author never sees that file. A line number
   * that points into generated boilerplate is worse than none, because it is read as a fact about
   * the author's own code.
   */
  @Test
  void positionsCompileErrorsInTheSourceTheCallerActuallySent() {
    MethodPatchService.Result result =
        compile(
            "greet(String)",
            "public static String greet(String name) {\n" + "    return missingSymbol;\n" + "}");

    assertFalse(result.isSuccess());
    @SuppressWarnings("unchecked")
    List<Map<String, Object>> diagnostics =
        (List<Map<String, Object>>) result.toMap().get("diagnostics");
    Map<String, Object> first = diagnostics.get(0);

    assertEquals("ERROR", first.get("kind"));
    assertEquals(
        2L,
        ((Number) first.get("line")).longValue(),
        () ->
            "the error is on line 2 of what the caller sent, not of the skeleton: " + diagnostics);
    assertTrue(
        String.valueOf(first.get("message")).contains("missingSymbol"),
        () -> String.valueOf(first));
  }

  /**
   * A lambda compiles to a synthetic method, and a redefinition cannot add one. The JVM's own
   * refusal is an {@code UnsupportedOperationException} that names nothing, so the construct has to
   * be named here.
   */
  @Test
  void refusesABodyThatWouldNeedAMethodTheJvmCannotAdd() {
    MethodPatchService.Result result =
        compile(
            "greet(String)",
            "public static String greet(String name) {\n"
                + "    java.util.function.Supplier<String> value = () -> \"howdy \" + name;\n"
                + "    return value.get();\n"
                + "}");

    assertTrue(result.isSuccess(), "the body compiles; it is the redefinition that cannot take it");
    IllegalStateException thrown =
        assertThrows(
            IllegalStateException.class,
            () -> service.splice(CLASS_NAME, result, ProbeFixture.class.getClassLoader()));

    assertTrue(thrown.getMessage().contains("synthetic methods"), () -> thrown.getMessage());
    assertTrue(
        thrown.getMessage().contains("lambda"),
        () -> "the message must name the construct to rewrite: " + thrown.getMessage());
  }

  /**
   * HotSpot rejects a modifier change outright, so catching it here is the difference between a
   * message about the declaration and one about nothing.
   */
  @Test
  void refusesAReplacementThatChangesTheMethodsModifiers() {
    MethodPatchService.Result result =
        compile(
            "greet(String)",
            "public static synchronized String greet(String name) { return name; }");

    assertTrue(result.isSuccess());
    IllegalArgumentException thrown =
        assertThrows(
            IllegalArgumentException.class,
            () -> service.splice(CLASS_NAME, result, ProbeFixture.class.getClassLoader()));

    assertTrue(thrown.getMessage().contains("different modifiers"), () -> thrown.getMessage());
    assertTrue(thrown.getMessage().contains("synchronized"), () -> thrown.getMessage());
  }

  @Test
  void refusesToSpliceASourceThatDeclaresADifferentMethod() {
    MethodPatchService.Result result =
        compile(
            "greet(String)", "public static String greet(String name, int times) { return name; }");

    // javac is happy: the skeleton has both the generated greet and this one.
    assertTrue(result.isSuccess());
    IllegalArgumentException thrown =
        assertThrows(
            IllegalArgumentException.class,
            () -> service.splice(CLASS_NAME, result, ProbeFixture.class.getClassLoader()));

    assertTrue(thrown.getMessage().contains("does not declare greet"), () -> thrown.getMessage());
  }
}
