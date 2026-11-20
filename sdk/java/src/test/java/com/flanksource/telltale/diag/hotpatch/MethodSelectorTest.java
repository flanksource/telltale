package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.fixture.ProbeFixture;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;
import java.util.NoSuchElementException;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.objectweb.asm.ClassReader;
import org.objectweb.asm.Opcodes;
import org.objectweb.asm.commons.Method;

/**
 * The four forms a method gets written in, and the one descriptor each resolves to.
 *
 * <p>Both ends of this matter. Accepting a pasted signature is why an author can copy a line out of
 * a stack trace instead of hand-writing a JVM descriptor; refusing an ambiguous one is why a patch
 * cannot land on an overload nobody named — which would report success and change the wrong code.
 */
final class MethodSelectorTest {

  private static ClassReader fixture() {
    return new ClassReader(LiveClass.bytecodeOf(ProbeFixture.class));
  }

  private static String resolve(String selector) {
    Method resolved =
        MethodSelector.parse(selector).resolve(ProbeFixture.class.getName(), fixture());
    return resolved.getName() + resolved.getDescriptor();
  }

  @ParameterizedTest(name = "{0} -> {1}")
  @CsvSource(
      quoteCharacter = '\'',
      value = {
        // A bare name, when only one method carries it.
        "'greet', 'greet(Ljava/lang/String;)Ljava/lang/String;'",
        // Simple parameter types, which is what a reader writes.
        "'describe(int)', 'describe(I)Ljava/lang/String;'",
        "'describe(String)', 'describe(Ljava/lang/String;)Ljava/lang/String;'",
        // An empty list means no parameters, not any arity.
        "'touch()', 'touch()V'",
        // A pasted Java signature: modifiers, return type and parameter names
        // are all discarded.
        "'public String describe(String value)', 'describe(Ljava/lang/String;)Ljava/lang/String;'",
        "'public String describe(final String v)',"
            + " 'describe(Ljava/lang/String;)Ljava/lang/String;'",
        // A JVM descriptor, used verbatim.
        "'describe(I)Ljava/lang/String;', 'describe(I)Ljava/lang/String;'",
        // A constructor, which has no name a bare identifier could carry.
        "'<init>(String)', '<init>(Ljava/lang/String;)V'",
        // Fully-qualified and primitive parameter types.
        "'total(long)', 'total(J)J'",
        "'greet(java.lang.String)', 'greet(Ljava/lang/String;)Ljava/lang/String;'",
      })
  void resolvesEveryFormAnAuthorWritesToOneDescriptor(String selector, String expected) {
    assertEquals(expected, resolve(selector));
  }

  @ParameterizedTest(name = "{0}")
  @CsvSource(
      quoteCharacter = '\'',
      value = {
        // Generic arguments are erased in a descriptor, so they are dropped.
        "'find(List<String> ids)', '(Ljava/util/List;)V'",
        // Varargs and arrays are the same descriptor.
        "'find(String... parts)', '([Ljava/lang/String;)V'",
        "'find(String[] parts)', '([Ljava/lang/String;)V'",
        "'find(int[][] grid)', '([[I)V'",
        // An annotated parameter, as it appears in real source.
        "'find(@Nullable String id)', '(Ljava/lang/String;)V'",
        // A nested type is written by its simple name, like everything else.
        "'find(Map.Entry entry)', '(Ljava/util/Map$Entry;)V'",
      })
  void readsTheParameterFormsThatAppearInRealSource(String selector, String descriptor) {
    assertTrue(
        MethodSelector.parse(selector).matches(descriptor),
        () -> selector + " should match " + descriptor);
  }

  /** The example this whole feature started from, pasted as it was written. */
  @Test
  void readsAPastedSignatureCompleteWithModifiersGenericsAndParameterNames() {
    MethodSelector selector =
        MethodSelector.parse(
            "private static String buildFindNextPendingActivityDclByClientGuidQuery("
                + "ActivityFilterDcl activityFilterDcl, List<String> excludeActivityGuidList,"
                + " String activityGuid)");

    assertEquals("buildFindNextPendingActivityDclByClientGuidQuery", selector.getName());
    assertEquals(
        "buildFindNextPendingActivityDclByClientGuidQuery(" + "ActivityFilterDcl, List, String)",
        selector.toString());
    assertTrue(
        selector.matches(
            "(Lexample/application/pas/dcl/ActivityFilterDcl;Ljava/util/List;"
                + "Ljava/lang/String;)Ljava/lang/String;"));
  }

  /** A bare name matches any arity; a written list does not. */
  @Test
  void distinguishesNoParameterListFromAnEmptyOne() {
    assertTrue(MethodSelector.parse("touch").matches("(Ljava/lang/String;)V"));
    assertFalse(MethodSelector.parse("touch()").matches("(Ljava/lang/String;)V"));
    assertTrue(MethodSelector.parse("touch()").matches("()V"));
  }

  /** A descriptor is exact: it is the one form that cannot mean two methods. */
  @Test
  void treatsADescriptorAsExact() {
    MethodSelector selector = MethodSelector.parse("describe(I)Ljava/lang/String;");

    assertTrue(selector.matches("(I)Ljava/lang/String;"));
    assertFalse(
        selector.matches("(I)Ljava/lang/Object;"), "the return type is part of a descriptor");
    assertFalse(selector.matches("(J)Ljava/lang/String;"));
  }

  @Test
  void refusesAnOverloadedNameAndNamesTheOverloads() {
    IllegalArgumentException thrown =
        assertThrows(IllegalArgumentException.class, () -> resolve("describe"));

    assertTrue(thrown.getMessage().contains("matches 2 overloads"), () -> thrown.getMessage());
    assertTrue(
        thrown.getMessage().contains("describe(I)Ljava/lang/String;"), () -> thrown.getMessage());
  }

  @Test
  void namesWhatTheClassDoesDeclareWhenNothingMatches() {
    NoSuchElementException thrown =
        assertThrows(NoSuchElementException.class, () -> resolve("greetings"));

    assertTrue(
        thrown.getMessage().contains("declares no method named greetings"),
        () -> thrown.getMessage());
    assertTrue(
        thrown.getMessage().contains("greet"),
        () -> "the message must list the real names: " + thrown.getMessage());
  }

  @Test
  void namesTheOverloadsWhenTheNameMatchesButTheParametersDoNot() {
    NoSuchElementException thrown =
        assertThrows(NoSuchElementException.class, () -> resolve("describe(double)"));

    assertTrue(
        thrown.getMessage().contains("has no describe matching describe(double)"),
        () -> thrown.getMessage());
    assertTrue(
        thrown.getMessage().contains("describe(Ljava/lang/String;)Ljava/lang/String;"),
        () -> thrown.getMessage());
  }

  @ParameterizedTest
  @CsvSource(
      quoteCharacter = '\'',
      value = {"''", "'   '", "'()'", "'greet(String'"})
  void refusesASelectorItCannotRead(String selector) {
    assertThrows(IllegalArgumentException.class, () -> MethodSelector.parse(selector));
  }

  // ------------------------------------------------------------- patterns

  /** The methods a selector ARMS, sorted; a skipped match is not one of them. */
  private static List<String> resolveAll(String selector) {
    List<String> resolved = new ArrayList<String>();
    for (MethodSelector.Match match :
        MethodSelector.parse(selector).resolveAll(ProbeFixture.class.getName(), fixture())) {
      if (match.isInstrumentable()) {
        resolved.add(match.getMethod().getName() + match.getMethod().getDescriptor());
      }
    }
    Collections.sort(resolved);
    return resolved;
  }

  /**
   * A pattern is how an author asks for a SET, and it is the only way to ask.
   *
   * <p>The cost a trace is measuring is often spread across overloads — the preservation plan names
   * {@code findNextPendingActivity*} for exactly that reason — and one method of four answers a
   * different question than the one asked.
   */
  @Test
  void resolvesAPatternToEveryMethodItNames() {
    assertEquals(
        Arrays.asList(
            "describe(I)Ljava/lang/String;", "describe(Ljava/lang/String;)Ljava/lang/String;"),
        resolveAll("describe*"));
  }

  /**
   * `name(*)` is every overload of exactly that name.
   *
   * <p>The form exists because the alternatives are both wrong. A bare name refuses, and rightly —
   * it is a request for one method. A name glob (`describe*`) is a DIFFERENT query: it would also
   * reach describeAll, describeBrief and anything else sharing the prefix, so using it to mean "the
   * overloads" silently instruments methods nobody named. That is the defect this parser already
   * refuses in the other direction.
   *
   * <p>This is the shape the preservation plan needs for ActivityBll.process, which is overloaded
   * four ways and wants all four.
   */
  @Test
  void resolvesEveryOverloadOfExactlyOneNameWhenTheParameterListIsAWildcard() {
    assertEquals(
        Arrays.asList(
            "describe(I)Ljava/lang/String;", "describe(Ljava/lang/String;)Ljava/lang/String;"),
        resolveAll("describe(*)"));
  }

  /** `(*)` fixes the name exactly — it is not the prefix a glob would match. */
  @Test
  void doesNotReachANameThatMerelyStartsTheSameWay() {
    assertEquals(
        Collections.singletonList("greet(Ljava/lang/String;)Ljava/lang/String;"),
        resolveAll("greet(*)"),
        "greet(*) is greet's overloads, not everything beginning with greet");
  }

  /** A pattern narrowed by parameter types still filters within the set. */
  @Test
  void appliesTheParameterFilterInsideAPattern() {
    assertEquals(
        Collections.singletonList("describe(I)Ljava/lang/String;"), resolveAll("describe*(int)"));
  }

  /**
   * A bare overloaded name still refuses, and that is deliberate rather than an omission.
   *
   * <p>Silently instrumenting five overloads nobody asked for is the same defect as silently
   * instrumenting one of four — doing something other than what was written, and reporting success.
   * A pattern is the explicit request for a set; a name is a request for a method.
   */
  @Test
  void keepsRefusingABareOverloadedNameNowThatSetsExist() {
    IllegalArgumentException thrown =
        assertThrows(IllegalArgumentException.class, () -> resolveAll("describe"));

    assertTrue(thrown.getMessage().contains("matches 2 overloads"), () -> thrown.getMessage());
  }

  /**
   * A pattern that matches nothing is a typo, and it has to say so.
   *
   * <p>Returning an empty set would arm a span over no methods at all, which reports clean and
   * captures nothing — the exact "the method never ran" reading this whole subsystem exists to make
   * impossible.
   */
  @Test
  void refusesAPatternThatMatchesNothingRatherThanArmingAnEmptySet() {
    NoSuchElementException thrown =
        assertThrows(NoSuchElementException.class, () -> resolveAll("findNextPending*"));

    assertTrue(thrown.getMessage().contains("findNextPending*"), () -> thrown.getMessage());
    assertTrue(
        thrown.getMessage().contains("greet"),
        () -> "the message must list what is really there: " + thrown.getMessage());
  }

  /**
   * A pattern never reaches a constructor, a static initialiser or a synthetic method.
   *
   * <p>{@code *} would otherwise match {@code <clinit>} — instrumenting the one method whose
   * failure is an {@code ExceptionInInitializerError} that breaks the class for the life of the JVM
   * — and every {@code lambda$...} body and bridge the compiler emitted, none of which anyone wrote
   * or would recognise in a trace.
   */
  @Test
  void leavesConstructorsInitialisersAndCompilerGeneratedMethodsOutOfAPattern() {
    for (String resolved : resolveAll("*")) {
      assertFalse(resolved.startsWith("<"), () -> "a pattern must not reach " + resolved);
      assertFalse(
          resolved.contains("lambda$"),
          () -> "a pattern must not reach a synthetic method: " + resolved);
    }
    assertTrue(
        resolveAll("*").contains("greet(Ljava/lang/String;)Ljava/lang/String;"),
        "the methods someone actually wrote are still there");
  }

  /**
   * A pattern that arms more methods than the cap refuses, naming the count.
   *
   * <p>Every instrumented method is probe calls on a live JVM's hot path and a share of a bounded
   * ring. A pattern that hit two hundred of them would be a mistake nobody meant to make, and
   * finding out by watching the JVM slow down is the wrong way to learn it.
   */
  @Test
  void refusesAPatternThatWouldArmMoreMethodsThanTheCap() {
    List<Method> many = new ArrayList<Method>();
    for (int i = 0; i <= MethodSelector.MAX_PATTERN_METHODS; i++) {
      many.add(new Method("run" + i, "()V"));
    }

    IllegalArgumentException thrown =
        assertThrows(
            IllegalArgumentException.class,
            () -> MethodSelector.parse("run*").resolveAll("com.example.Wide", declared(many)));

    assertTrue(
        thrown.getMessage().contains(String.valueOf(many.size())), () -> thrown.getMessage());
    assertTrue(
        thrown.getMessage().contains(String.valueOf(MethodSelector.MAX_PATTERN_METHODS)),
        () -> "the refusal must name the cap so it can be argued with: " + thrown.getMessage());
  }

  /**
   * A match with no body comes back marked, not dropped.
   *
   * <p>Dropping it makes "you named four, three were armed" look exactly like "you named three":
   * the caller receives a list of three either way and cannot tell that a quarter of what it asked
   * for is not being watched. Patterns over an interface's implementations hit abstract
   * declarations routinely, so this is the ordinary case rather than the exotic one.
   */
  @Test
  void reportsAMatchItCannotArmRatherThanDroppingItFromTheSet() {
    List<MethodSelector.Declared> declared =
        Arrays.asList(
            new MethodSelector.Declared(0, new Method("runFast", "()V")),
            new MethodSelector.Declared(Opcodes.ACC_ABSTRACT, new Method("runSlow", "()V")),
            new MethodSelector.Declared(Opcodes.ACC_NATIVE, new Method("runNative", "()V")));

    List<MethodSelector.Match> matched =
        MethodSelector.parse("run*").resolveAll("com.example.Mixed", declared);

    assertEquals(3, matched.size(), "every method the pattern named is accounted for");
    assertTrue(matched.get(0).isInstrumentable());
    assertEquals("abstract", matched.get(1).getSkipped());
    assertEquals("native", matched.get(2).getSkipped());
  }

  /**
   * A pattern whose every match has no body refuses, rather than arming an empty set and reporting
   * a capture that can never record anything.
   */
  @Test
  void refusesWhenEveryMethodAPatternNamesHasNoBody() {
    List<MethodSelector.Declared> declared =
        Collections.singletonList(
            new MethodSelector.Declared(Opcodes.ACC_ABSTRACT, new Method("runSlow", "()V")));

    NoSuchElementException thrown =
        assertThrows(
            NoSuchElementException.class,
            () -> MethodSelector.parse("run*").resolveAll("com.example.Abstract", declared));

    assertTrue(thrown.getMessage().contains("abstract"), () -> thrown.getMessage());
  }

  private static List<MethodSelector.Declared> declared(List<Method> methods) {
    List<MethodSelector.Declared> out = new ArrayList<MethodSelector.Declared>();
    for (Method method : methods) {
      out.add(new MethodSelector.Declared(0, method));
    }
    return out;
  }
}
