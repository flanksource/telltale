package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.fixture.ProbeFixture;
import com.flanksource.telltale.diag.fixture.SkeletonFixture;
import org.junit.jupiter.api.Test;
import org.objectweb.asm.ClassReader;
import org.objectweb.asm.commons.Method;

/**
 * Compiles what the generator produced.
 *
 * <p>That is the assertion, not a string comparison: a skeleton exists to be compiled, so "javac
 * accepted it" is the only property worth checking, and it catches every way the generator can be
 * wrong at once — a dropped type parameter, an unqualified nested type, a superclass whose
 * constructor was never called. A few specific strings are asserted alongside it only so a
 * regression says which of those it was.
 */
final class SkeletonServiceTest {

  private final SkeletonService skeletons = new SkeletonService();
  private final CompilerService compiler = new CompilerService();

  private static Method resolve(Class<?> target, String selector) {
    return MethodSelector.parse(selector)
        .resolve(target.getName(), new ClassReader(LiveClass.bytecodeOf(target)));
  }

  private SkeletonService.Skeleton skeleton(Class<?> target, String selector, String source) {
    return skeletons.skeleton(target, source, resolve(target, selector));
  }

  private void assertCompiles(Class<?> target, SkeletonService.Skeleton skeleton) {
    CompilerService.Result result =
        compiler.compile(
            new CompilerService.Request(target.getName(), skeleton.getSource(), target));
    assertTrue(
        result.isSuccess(),
        () ->
            "the skeleton must compile, but javac said "
                + result.getMessages()
                + "\n---\n"
                + skeleton.getSource());
  }

  /**
   * The hard case, and the one this exists for: a bounded type parameter, an interface, a
   * superclass with no no-argument constructor, final fields, a nested type in a parameter list,
   * varargs, and a method-level type parameter.
   */
  @Test
  void generatesACompilableStandInForAGenericClassWithANonTrivialSuperclass() {
    SkeletonService.Skeleton skeleton =
        skeleton(
            SkeletonFixture.class,
            "convert(Comparable, Object)",
            "protected <R> R convert(T value, R fallback) throws IllegalStateException {\n"
                + "    return fallback;\n"
                + "}");

    assertCompiles(SkeletonFixture.class, skeleton);
    assertTrue(
        skeleton.getSource().contains("super((java.lang.String) null, (int) 0);"),
        () ->
            "the superclass has no no-argument constructor, so super() must be explicit:\n"
                + skeleton.getSource());
    assertTrue(
        skeleton.getSource().contains("class SkeletonFixture<T extends java.lang.Comparable<T>>"),
        () -> skeleton.getSource());
    assertTrue(
        skeleton.getSource().contains("java.util.Map.Entry<java.lang.String, T> p0"),
        () -> "a nested type must be written canonically, not with '$':\n" + skeleton.getSource());
    assertTrue(
        skeleton.getSource().contains("int... p1"),
        () -> "varargs must stay varargs, or the descriptor changes:\n" + skeleton.getSource());
  }

  /**
   * {@code final} is dropped because nothing here is ever constructed: keeping it would demand an
   * initialiser for a field that exists only so the replacement method can name it.
   */
  @Test
  void dropsFinalFromFieldsSoNoInitialiserIsNeeded() {
    SkeletonService.Skeleton skeleton =
        skeleton(
            SkeletonFixture.class,
            "getItems()",
            "public java.util.List<T> getItems() { return null; }");

    assertTrue(
        skeleton.getSource().contains("private java.util.List<T> items;"),
        () -> skeleton.getSource());
    assertFalse(
        skeleton.getSource().contains("final java.util.List<T> items"), () -> skeleton.getSource());
    assertCompiles(SkeletonFixture.class, skeleton);
  }

  /** The replaced method is the author's, not a generated stand-in for it. */
  @Test
  void leavesOutTheMethodBeingReplacedAndKeepsEveryOther() {
    SkeletonService.Skeleton skeleton =
        skeleton(
            ProbeFixture.class,
            "greet(String)",
            "public static String greet(String name) { return \"howdy \" + name; }");

    assertTrue(
        skeleton.getSource().contains("return \"howdy \" + name;"), () -> skeleton.getSource());
    assertFalse(
        skeleton.getSource().contains("static java.lang.String greet(java.lang.String p0)"),
        () -> "the replaced method must not also be generated:\n" + skeleton.getSource());
    assertTrue(
        skeleton.getSource().contains("public int add(int p0, int p1)"),
        () -> skeleton.getSource());
    assertCompiles(ProbeFixture.class, skeleton);
  }

  /** The offset is what lets a compile error be reported against the author's own lines. */
  @Test
  void reportsWhereTheAuthorsSourceStartsInTheGeneratedFile() {
    SkeletonService.Skeleton skeleton =
        skeleton(
            ProbeFixture.class,
            "greet(String)",
            "public static String greet(String name) { return name; }");

    String[] lines = skeleton.getSource().split("\n", -1);
    assertTrue(
        lines[skeleton.getMethodLine() - 1].startsWith("public static String greet"),
        () ->
            "line "
                + skeleton.getMethodLine()
                + " is \""
                + lines[skeleton.getMethodLine() - 1]
                + "\" in:\n"
                + skeleton.getSource());
  }

  /**
   * An enum's constants and an interface's lack of a constructor are each a different generator;
   * refusing names the limit and points at the path that has no such restriction.
   */
  @Test
  void refusesAKindOfClassItCannotStandInFor() {
    IllegalArgumentException thrown =
        assertThrows(
            IllegalArgumentException.class,
            () ->
                skeletons.skeleton(
                    java.lang.annotation.RetentionPolicy.class,
                    "int x() { return 1; }",
                    new Method("x", "()I")));

    assertTrue(thrown.getMessage().contains("is an enum"), () -> thrown.getMessage());
    assertTrue(
        thrown.getMessage().contains("jvm.trace"),
        () -> "the refusal must name the path that does work: " + thrown.getMessage());
  }
}
