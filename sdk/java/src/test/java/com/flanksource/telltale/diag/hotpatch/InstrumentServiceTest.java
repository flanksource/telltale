package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.fixture.ProbeFixture;
import com.flanksource.telltale.diag.fixture.Probes;
import java.lang.reflect.Constructor;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Instruments a fixture, defines the rewritten bytes and runs them.
 *
 * <p>Running is the assertion that matters: the JVM verifies the recomputed stack map frames as it
 * defines the class, so a transform that produced unverifiable bytes fails here rather than in the
 * pod. What the method returned is checked alongside what it reported, because instrumentation that
 * changed the answer would be worse than no instrumentation at all.
 */
final class InstrumentServiceTest {

  private static final String CLASS_NAME = ProbeFixture.class.getName();

  private final InstrumentService service = new InstrumentService();

  @BeforeEach
  void clearProbes() {
    Probes.closeAll();
  }

  private InstrumentService.Request request(String selector, String key) {
    return new InstrumentService.Request(
        CLASS_NAME,
        LiveClass.bytecodeOf(ProbeFixture.class),
        ProbeFixture.class.getClassLoader(),
        MethodSelector.parse(selector),
        key);
  }

  /**
   * Instruments, opens the buffer and defines the class — in that order, mirroring the servlet,
   * which opens the probe before it redefines so a rewritten class never records into a buffer that
   * is not there.
   */
  private Class<?> live(InstrumentService.Request request, String key)
      throws ClassNotFoundException {
    InstrumentService.Result result = service.instrument(request);
    Probes.open(key);
    return LiveClass.define(CLASS_NAME, result.getBytecode());
  }

  @Test
  void reportsEntryArgumentsReturnValueAndTimingWithoutChangingTheAnswer() throws Exception {
    String key = "ProbeFixture.greet.a1";
    InstrumentService.Result result =
        service.instrument(request("greet(String)", key).withArgs(true).withReturn(true));

    assertEquals(
        "(Ljava/lang/String;)Ljava/lang/String;",
        result.getDescriptor(),
        "the resolved descriptor is what lets the caller detect a wrong overload");

    Probes.open(key);
    Class<?> instrumented = LiveClass.define(CLASS_NAME, result.getBytecode());
    Method greet = instrumented.getMethod("greet", String.class);
    assertEquals("hello world", greet.invoke(null, "world"));

    assertEquals(Arrays.asList("enter", "return", "exit"), Probes.kinds(key));
    Map<String, Object> entered = Probes.first(key, "enter");
    assertEquals(CLASS_NAME, entered.get("class"));
    assertEquals("greet", entered.get("method"));
    assertEquals(Arrays.asList("world"), Probes.argValues(entered));
    assertEquals("hello world", Probes.scalar(Probes.first(key, "return").get("value")));
  }

  @Test
  void recordsNothingAboutArgumentsOrReturnValuesWhenNeitherWasAskedFor() throws Exception {
    String key = "ProbeFixture.greet.a2";
    Class<?> instrumented = live(request("greet", key), key);
    assertEquals(
        "hello world", instrumented.getMethod("greet", String.class).invoke(null, "world"));

    assertEquals(Arrays.asList("enter", "exit"), Probes.kinds(key));
    assertNull(Probes.first(key, "enter").get("args"), "no argument list without --args");
  }

  /**
   * A two-slot return value is the shape a naive dup/swap would corrupt: the probe has to hand back
   * exactly what the method computed.
   */
  @Test
  void preservesATwoSlotReturnValue() throws Exception {
    String key = "ProbeFixture.total.a1";
    Class<?> instrumented = live(request("total(long)", key).withReturn(true), key);
    Object value =
        instrumented
            .getMethod("total", long.class)
            .invoke(instrumented.getConstructor(String.class).newInstance("p"), 21L);

    assertEquals(42L, value);
    assertEquals("42", Probes.scalar(Probes.first(key, "return").get("value")));
  }

  /** The other two-slot shape, and a different {@code box} path from long. */
  @Test
  void preservesADoubleReturnValue() throws Exception {
    String key = "ProbeFixture.rate.a1";
    Class<?> instrumented = live(request("rate(double)", key).withReturn(true), key);
    Object value =
        instrumented
            .getMethod("rate", double.class)
            .invoke(instrumented.getConstructor(String.class).newInstance("p"), 1.5d);

    assertEquals(3.0d, value);
    assertEquals("3.0", Probes.scalar(Probes.first(key, "return").get("value")));
  }

  @Test
  void reportsAVoidMethodAsEnteredAndTimedWithNoReturnFrame() throws Exception {
    String key = "ProbeFixture.touch.a1";
    Class<?> instrumented = live(request("touch()", key).withReturn(true), key);
    instrumented
        .getMethod("touch")
        .invoke(instrumented.getConstructor(String.class).newInstance("p"));

    assertEquals(
        Arrays.asList("enter", "exit"), Probes.kinds(key), "a void method has nothing to return");
  }

  /**
   * The exception has to reach the caller unchanged — a probe that swallowed or wrapped it would
   * change the behaviour of the code being observed.
   */
  @Test
  void reportsAPropagatingExceptionAndRethrowsItUnchanged() throws Exception {
    String key = "ProbeFixture.boom.a1";
    Class<?> instrumented = live(request("boom()", key).withException(true), key);
    Object fixture = instrumented.getConstructor(String.class).newInstance("p");
    Method boom = instrumented.getMethod("boom");

    InvocationTargetException thrown =
        assertThrows(InvocationTargetException.class, () -> boom.invoke(fixture));
    assertEquals(IllegalStateException.class, thrown.getCause().getClass());
    assertEquals("boom", thrown.getCause().getMessage());

    assertEquals(Arrays.asList("enter", "throw"), Probes.kinds(key));
    assertEquals("java.lang.IllegalStateException: boom", Probes.first(key, "throw").get("error"));
  }

  /**
   * The frame closes even when exception detail was not asked for.
   *
   * <p>This is not a nicety. Depth is tracked per thread, so an entry with no matching close leaves
   * the depth permanently raised on a pooled request thread, and every later trace on that thread
   * reports nesting that never happened. {@code --exception} controls how much is said about the
   * throwable, never whether the frame is closed.
   */
  @Test
  void closesTheFrameOnAThrowEvenWhenExceptionCaptureWasNotAskedFor() throws Exception {
    String key = "ProbeFixture.boom.a2";
    Class<?> instrumented = live(request("boom()", key), key);
    Object fixture = instrumented.getConstructor(String.class).newInstance("p");
    Method boom = instrumented.getMethod("boom");

    assertThrows(InvocationTargetException.class, () -> boom.invoke(fixture));

    assertEquals(
        Arrays.asList("enter", "throw"),
        Probes.kinds(key),
        "the frame must close on the way out even without --exception");
  }

  /**
   * A method's own handlers keep winning: ours is registered from visitMaxs, after the method's
   * were visited, so ASM writes it last in the exception table and it only sees what the method did
   * not catch.
   */
  @Test
  void keepsTheMethodsOwnCatchBlocksAheadOfTheProbeHandler() throws Exception {
    String key = "ProbeFixture.swallow.a1";
    Class<?> instrumented = live(request("swallow()", key).withReturn(true), key);
    Object fixture = instrumented.getConstructor(String.class).newInstance("p");

    assertEquals("caught", instrumented.getMethod("swallow").invoke(fixture));
    assertEquals(
        Arrays.asList("enter", "return", "exit"),
        Probes.kinds(key),
        "an exception the method handles itself is not a throw for the probe");
  }

  /**
   * A lambda in the body is ordinary here, and this is the capability that justifies transforming
   * live bytecode rather than recompiling decompiled source — the source path has to refuse such a
   * body outright.
   */
  @Test
  void instrumentsAMethodWhoseBodyContainsALambda() throws Exception {
    String key = "ProbeFixture.labels.a1";
    Class<?> instrumented = live(request("labels(List)", key).withReturn(true), key);
    Object fixture = instrumented.getConstructor(String.class).newInstance("p");

    Object value =
        instrumented.getMethod("labels", List.class).invoke(fixture, Arrays.asList("b", "a"));
    assertEquals("p:a,p:b", value, "the lambda still runs, and still sorts");
    assertEquals("p:a,p:b", Probes.scalar(Probes.first(key, "return").get("value")));
  }

  /** A method reference is a different invokedynamic shape from a lambda. */
  @Test
  void instrumentsAMethodWhoseBodyContainsAMethodReference() throws Exception {
    String key = "ProbeFixture.upper.a1";
    Class<?> instrumented = live(request("upper(List)", key).withReturn(true), key);
    Object fixture = instrumented.getConstructor(String.class).newInstance("p");

    assertEquals(
        "A,B",
        instrumented.getMethod("upper", List.class).invoke(fixture, Arrays.asList("a", "b")));
    assertEquals("A,B", Probes.scalar(Probes.first(key, "return").get("value")));
  }

  /**
   * A probe placed before the {@code super()} call would see an uninitialised {@code this} and be
   * rejected by the verifier, so defining the class at all is most of this assertion.
   */
  @Test
  void instrumentsAConstructorAfterItsSuperCall() throws Exception {
    String key = "ProbeFixture.init.a1";
    Class<?> instrumented = live(request("<init>(String)", key).withArgs(true), key);
    Constructor<?> constructor = instrumented.getConstructor(String.class);
    Object fixture = constructor.newInstance("p-");

    assertEquals(
        "p-x",
        instrumented.getMethod("prefixed", String.class).invoke(fixture, "x"),
        "the constructor still has to construct");
    Map<String, Object> entered = Probes.first(key, "enter");
    assertEquals("<init>", entered.get("method"));
    assertEquals(Arrays.asList("p-"), Probes.argValues(entered));
  }

  /** Only the named method is touched; everything else keeps the bytes it had. */
  @Test
  void leavesEveryOtherMethodExactlyAsItWas() throws Exception {
    String key = "ProbeFixture.greet.a3";
    Class<?> instrumented = live(request("greet", key), key);
    Object fixture = instrumented.getConstructor(String.class).newInstance("p");

    assertEquals(5, instrumented.getMethod("add", int.class, int.class).invoke(fixture, 2, 3));
    assertTrue(Probes.events(key).isEmpty(), "add() must report nothing; it was not instrumented");
  }

  /**
   * Nesting is recorded where it is known — on the thread — because arrival order alone cannot tell
   * an inner call from the next outer one.
   */
  @Test
  void reportsDepthAndParentWhenAnInstrumentedMethodCallsAnother() throws Exception {
    String outerKey = "ProbeFixture.outer.a1";
    String innerKey = "ProbeFixture.inner.a1";

    // Both methods live in one class, so they are instrumented into one set
    // of bytes and defined together — two separate defines would give the
    // caller the original inner method.
    InstrumentService.Result first = service.instrument(request("outer()", outerKey));
    InstrumentService.Result both =
        service.instrument(
            new InstrumentService.Request(
                CLASS_NAME,
                first.getBytecode(),
                ProbeFixture.class.getClassLoader(),
                MethodSelector.parse("inner()"),
                innerKey));
    Probes.open(outerKey);
    Probes.open(innerKey);

    Class<?> instrumented = LiveClass.define(CLASS_NAME, both.getBytecode());
    Object fixture = instrumented.getConstructor(String.class).newInstance("p");
    instrumented.getMethod("outer").invoke(fixture);

    Map<String, Object> outer = Probes.first(outerKey, "enter");
    Map<String, Object> inner = Probes.first(innerKey, "enter");
    assertEquals(0, ((Number) outer.get("depth")).intValue(), "outer is the outermost frame");
    assertEquals(1, ((Number) inner.get("depth")).intValue(), "inner is one deep");
    assertEquals(
        outer.get("frame"),
        inner.get("parent"),
        "the inner frame must point at the frame that called it");
    assertEquals(outer.get("threadId"), inner.get("threadId"));
  }

  /**
   * A pattern arms every method it names, in ONE rewrite, into ONE ring.
   *
   * <p>One rewrite because a class is redefined whole: instrumenting the overloads one at a time
   * would mean re-reading the already-rewritten bytes for each, and the second pass would wrap the
   * first pass's probe calls. One ring because the events only sort into a single call tree if they
   * share a key — which is what makes the descriptor on each entry the thing that tells the
   * overloads apart.
   */
  @Test
  void armsEveryMethodOfAPatternInOneRewriteAndOneRing() throws Exception {
    String key = "ProbeFixture.describe.a5";
    InstrumentService.Result result =
        service.instrument(request("describe*", key).withReturn(true));

    assertEquals(
        Arrays.asList("(I)Ljava/lang/String;", "(Ljava/lang/String;)Ljava/lang/String;"),
        sorted(result.getDescriptors()),
        "the resolved set is the answer; one descriptor could not show a pattern that matched three"
            + " of four");

    Probes.open(key);
    Class<?> instrumented = LiveClass.define(CLASS_NAME, result.getBytecode());
    Object fixture = instrumented.getConstructor(String.class).newInstance("p");
    assertEquals("i:7", instrumented.getMethod("describe", int.class).invoke(fixture, 7));
    assertEquals("s:x", instrumented.getMethod("describe", String.class).invoke(fixture, "x"));

    List<String> descriptors = new ArrayList<>();
    for (Map<String, Object> event : Probes.events(key)) {
      if ("enter".equals(event.get("kind"))) {
        descriptors.add(String.valueOf(event.get("descriptor")));
      }
    }
    assertEquals(
        Arrays.asList("(I)Ljava/lang/String;", "(Ljava/lang/String;)Ljava/lang/String;"),
        descriptors,
        "both overloads recorded into the one ring, each saying which it was");
  }

  /** A method the pattern did not name keeps its original bytes. */
  @Test
  void leavesTheMethodsAPatternDidNotNameAlone() throws Exception {
    String key = "ProbeFixture.describe.a6";
    Class<?> instrumented = live(request("describe*", key), key);
    Object fixture = instrumented.getConstructor(String.class).newInstance("p");

    instrumented.getMethod("add", int.class, int.class).invoke(fixture, 1, 2);

    assertTrue(Probes.events(key).isEmpty(), "add was not named and must not record");
  }

  private static List<String> sorted(List<String> values) {
    List<String> out = new ArrayList<>(values);
    Collections.sort(out);
    return out;
  }

  /**
   * The call tree beneath an instrumented method, without instrumenting anything it calls.
   *
   * <p>This is the one capability arthas trace had and this did not, and it is the reason two of
   * the preservation plan's watch blocks could not migrate: "the only view of what they did on the
   * way" is the callees, and probes on the named method alone cannot see them.
   *
   * <p>It works by wrapping each invocation instruction in the CALLER's body rather than by
   * patching the callee, which is what makes it possible at all — the callees are not known until
   * the bytecode is read, they are spread across classes nobody named, and patching them would need
   * a lease and a restore for each.
   */
  @Test
  void reportsWhatAnInstrumentedMethodCalledWithoutInstrumentingTheCallee() throws Exception {
    String key = "ProbeFixture.outer.c1";
    Class<?> instrumented = live(request("outer", key).withCalls(true), key);
    Object fixture = instrumented.getConstructor(String.class).newInstance("p");

    assertEquals(
        "outer:inner",
        instrumented.getMethod("outer").invoke(fixture),
        "wrapping the call sites must not change what the method returns");

    List<Map<String, Object>> events = Probes.events(key);
    Map<String, Object> outer = Probes.first(key, "enter");
    Map<String, Object> inner = null;
    for (Map<String, Object> event : events) {
      if ("enter".equals(event.get("kind")) && "inner".equals(event.get("method"))) {
        inner = event;
      }
    }

    assertNotNull(inner, () -> "the call to inner() must be reported: " + Probes.kinds(key));
    assertEquals(CLASS_NAME, inner.get("class"), "the callee's declared owner at the call site");
    assertEquals("()Ljava/lang/String;", inner.get("descriptor"));
    assertEquals(
        outer.get("frame"), inner.get("parent"), "a call nests under the invocation that made it");
    assertEquals(1, ((Number) inner.get("depth")).intValue());
  }

  /**
   * A call site reports the line it was made from — the CALLER's line, which is what lets the trace
   * viewer show the code that made each call. The instrumented method's own entry has no caller in
   * view and reports none.
   */
  @Test
  void reportsTheCallersLineForEachCallSite() throws Exception {
    String key = "ProbeFixture.outer.c5";
    Class<?> instrumented = live(request("outer", key).withCalls(true), key);
    instrumented
        .getMethod("outer")
        .invoke(instrumented.getConstructor(String.class).newInstance("p"));

    Map<String, Object> inner = null;
    for (Map<String, Object> event : Probes.events(key)) {
      if ("enter".equals(event.get("kind")) && "inner".equals(event.get("method"))) {
        inner = event;
      }
    }
    assertNotNull(inner, () -> "the call to inner() must be reported: " + Probes.kinds(key));
    assertEquals(
        fixtureLineOf("return \"outer:\" + inner();"), ((Number) inner.get("line")).intValue());
    assertNull(Probes.first(key, "enter").get("line"), "outer's own entry has no call-site line");
  }

  /** The 1-based line of the fixture source holding {@code code}, read rather than hard-coded. */
  private static int fixtureLineOf(String code) throws java.io.IOException {
    List<String> lines =
        java.nio.file.Files.readAllLines(
            java.nio.file.Paths.get(
                "src/test/java/com/flanksource/telltale/diag/fixture/ProbeFixture.java"));
    for (int i = 0; i < lines.size(); i++) {
      if (lines.get(i).contains(code)) {
        return i + 1;
      }
    }
    throw new AssertionError("ProbeFixture.java has no line containing " + code);
  }

  /**
   * A method's own JDK calls are not what anyone is tracing.
   *
   * <p>{@code "outer:" + inner()} compiles to StringBuilder construction and three appends.
   * Reporting those would bury the one call that matters under noise nobody wrote, and on a hot
   * method it is the difference between a readable tree and a ring that evicts the real events to
   * make room for String.valueOf.
   */
  @Test
  void leavesTheJdkOutOfTheCallTree() throws Exception {
    String key = "ProbeFixture.outer.c2";
    Class<?> instrumented = live(request("outer", key).withCalls(true), key);
    instrumented
        .getMethod("outer")
        .invoke(instrumented.getConstructor(String.class).newInstance("p"));

    for (Map<String, Object> event : Probes.events(key)) {
      String owner = String.valueOf(event.get("class"));
      assertFalse(
          owner.startsWith("java.") || owner.startsWith("javax."),
          () -> "a JDK call must not be reported: " + owner + "." + event.get("method"));
    }
  }

  /**
   * The platform is left out by default and reported on request — arthas' {@code --skipJDKMethod},
   * inverted so the quiet answer is the default.
   *
   * <p>Worth having rather than assuming: what a method spends inside a JDK collection or a JDBC
   * driver is sometimes exactly the question, and without this the only way to ask it is to
   * instrument the callee by name.
   */
  @Test
  void reportsThePlatformWhenAskedFor() throws Exception {
    String key = "ProbeFixture.outer.c4";
    Class<?> instrumented =
        live(request("outer", key).withCalls(true).withPlatformCalls(true), key);
    instrumented
        .getMethod("outer")
        .invoke(instrumented.getConstructor(String.class).newInstance("p"));

    boolean sawPlatform = false;
    for (Map<String, Object> event : Probes.events(key)) {
      if (String.valueOf(event.get("class")).startsWith("java.")) {
        sawPlatform = true;
      }
    }
    assertTrue(sawPlatform, () -> "the StringBuilder calls must be reported: " + Probes.kinds(key));
  }

  /**
   * Generated proxy, aspect and lambda frames stay out either way.
   *
   * <p>Ported from arthas' {@code AsmUtils.isEnhancerByCGLIB} and {@code ClassUtils.isLambdaClass},
   * and unconditional for the same reason arthas keeps the box types unconditional: they are noise
   * whether or not the platform is being reported. A Spring AOP or CGLIB proxy sits between the
   * caller and the method that does the work, so tracing it describes the interception rather than
   * the call, and a lambda's synthetic class is named by a counter that changes between runs.
   */
  @Test
  void leavesGeneratedProxyAndAspectFramesOutOfTheCallTree() {
    assertFalse(
        InstrumentService.tracesCallsTo(
            "example/application/pas/bll/ActivityBll$$EnhancerBySpringCGLIB$$a1b2c3",
            "process",
            true));
    assertFalse(
        InstrumentService.tracesCallsTo(
            "example/application/pas/bll/ActivityBll$$EnhancerByCGLIB$$a1b2c3", "process", true));
    assertFalse(
        InstrumentService.tracesCallsTo(
            "example/application/pas/bll/ActivityBll$$FastClassBySpringCGLIB$$a1b2c3",
            "invoke",
            true));
    assertFalse(
        InstrumentService.tracesCallsTo(
            "example/application/pas/dal/ActivityDal$$Lambda$17", "apply", true));
    assertFalse(InstrumentService.tracesCallsTo("com/sun/proxy/$Proxy42", "invoke", true));
    assertFalse(
        InstrumentService.tracesCallsTo(
            "org/aspectj/runtime/reflect/JoinPointImpl", "proceed", true));

    // ...even with the platform explicitly asked for, which is the whole point
    // of them being a separate rule rather than part of the JDK prefix list.
    assertFalse(
        InstrumentService.tracesCallsTo(
            "example/application/pas/bll/ActivityBll$$EnhancerBySpringCGLIB$$a1b2c3",
            "process",
            false));
    assertFalse(InstrumentService.tracesCallsTo("com/sun/proxy/$Proxy42", "invoke", false));

    assertTrue(
        InstrumentService.tracesCallsTo(
            "example/application/pas/dal/ActivityDal", "buildQuery", true),
        "an application class is what the tree is for");
    assertFalse(InstrumentService.tracesCallsTo("java/lang/StringBuilder", "append", true));
    assertTrue(
        InstrumentService.tracesCallsTo("java/lang/StringBuilder", "append", false),
        "the platform comes back when it is asked for");
  }

  /**
   * application weaves its aspects at compile time, so the CGLIB markers never fire and the AspectJ
   * weaver's own artifacts appear instead.
   *
   * <p>The exact shapes below are copied from a live capture of {@code ActivityBll.process}:
   * closure constructors, {@code aspectOf} accessors and the weaver's renamed bodies took 83 of the
   * probe's 100 events, leaving 17 for the method being traced.
   */
  @Test
  void leavesTheAspectjWeaversOwnPlumbingOutOfTheCallTree() {
    assertFalse(
        InstrumentService.tracesCallsTo(
            "example/application/pas/bll/ActivityBll$AjcClosure1", "<init>", true));
    assertFalse(
        InstrumentService.tracesCallsTo(
            "example/application/pas/bll/aspects/SreForwardProcessingExtensibilityAspect",
            "aspectOf",
            true));
    assertFalse(
        InstrumentService.tracesCallsTo(
            "example/application/pas/bll/aspect/ActivityProcessingNotificationAspect",
            "hasAspect",
            true));
    assertFalse(
        InstrumentService.tracesCallsTo(
            "example/application/pas/bll/ActivityBll", "ajc$superDispatch$process", true));

    // The advice itself is code someone wrote, and it does real work — only
    // the machinery that dispatches to it is dropped.
    assertTrue(
        InstrumentService.tracesCallsTo(
            "example/application/pas/bll/aspect/ActivityProcessingNotificationAspect",
            "processCycleActivityProcessingNotificationMethod",
            true),
        "an aspect's advice body is behaviour, not plumbing");
    assertTrue(
        InstrumentService.tracesCallsTo(
            "example/application/pas/bll/aspects/SreForwardProcessingExtensibilityAspect",
            "instrumentActivityBll_process",
            true));
  }

  /** Without the flag nothing changes — the call sites are left alone. */
  @Test
  void reportsNoCallsUnlessAskedTo() throws Exception {
    String key = "ProbeFixture.outer.c3";
    Class<?> instrumented = live(request("outer", key), key);
    instrumented
        .getMethod("outer")
        .invoke(instrumented.getConstructor(String.class).newInstance("p"));

    assertEquals(
        Arrays.asList("enter", "exit"), Probes.kinds(key), "only the instrumented method itself");
  }

  @Test
  void refusesAnAmbiguousSelectorRatherThanPickingAnOverload() {
    IllegalArgumentException thrown =
        assertThrows(
            IllegalArgumentException.class,
            () -> service.instrument(request("describe", "ProbeFixture.describe.a1")));

    assertTrue(thrown.getMessage().contains("matches 2 overloads"), () -> thrown.getMessage());
    assertTrue(
        thrown.getMessage().contains("describe(I)Ljava/lang/String;")
            && thrown.getMessage().contains("describe(Ljava/lang/String;)Ljava/lang/String;"),
        () -> "the message must name the overloads so one can be chosen: " + thrown.getMessage());
  }

  @Test
  void resolvesTheNamedOverloadWhenTheSelectorCarriesParameterTypes() throws Exception {
    String key = "ProbeFixture.describe.a2";
    InstrumentService.Result result =
        service.instrument(request("describe(int)", key).withReturn(true));

    assertEquals("(I)Ljava/lang/String;", result.getDescriptor());
    Probes.open(key);
    Class<?> instrumented = LiveClass.define(CLASS_NAME, result.getBytecode());
    Object fixture = instrumented.getConstructor(String.class).newInstance("p");
    assertEquals("i:7", instrumented.getMethod("describe", int.class).invoke(fixture, 7));
    assertEquals("i:7", Probes.scalar(Probes.first(key, "return").get("value")));
  }

  /**
   * Every entry says which overload ran, not merely which name.
   *
   * <p>A method name is not an identity. Once one probe key covers a set of methods, {@code
   * "method":"describe"} on two events could be two overloads with different costs, and a rollup
   * keyed on the name averages them into a single row that describes neither. The descriptor is the
   * only thing on the event that separates them, so it rides on every entry rather than being
   * looked up from the instrument response — which knows one method per call and cannot answer for
   * a set.
   *
   * <p>It is the RAW JVM descriptor. The archive should hold what the JVM said; reducing it to the
   * simple names a person writes belongs to whatever renders it, and is not reversible once done.
   */
  @Test
  void recordsWhichOverloadRanAndNotJustItsName() throws Exception {
    String intKey = "ProbeFixture.describe.a3";
    String stringKey = "ProbeFixture.describe.a4";

    InstrumentService.Result ints = service.instrument(request("describe(int)", intKey));
    Probes.open(intKey);
    Class<?> instrumented = LiveClass.define(CLASS_NAME, ints.getBytecode());
    Object fixture = instrumented.getConstructor(String.class).newInstance("p");
    instrumented.getMethod("describe", int.class).invoke(fixture, 7);

    InstrumentService.Result strings =
        service.instrument(
            new InstrumentService.Request(
                CLASS_NAME,
                LiveClass.bytecodeOf(ProbeFixture.class),
                ProbeFixture.class.getClassLoader(),
                MethodSelector.parse("describe(String)"),
                stringKey));
    Probes.open(stringKey);
    Class<?> other = LiveClass.define(CLASS_NAME, strings.getBytecode());
    other
        .getMethod("describe", String.class)
        .invoke(other.getConstructor(String.class).newInstance("p"), "x");

    Map<String, Object> onInt = Probes.first(intKey, "enter");
    Map<String, Object> onString = Probes.first(stringKey, "enter");

    assertEquals("describe", onInt.get("method"));
    assertEquals("describe", onString.get("method"));
    assertEquals("(I)Ljava/lang/String;", onInt.get("descriptor"));
    assertEquals("(Ljava/lang/String;)Ljava/lang/String;", onString.get("descriptor"));
  }

  /**
   * An instrumented method whose probe was never opened records nothing and still behaves — the
   * buffer lookup is also the admission check, which is what makes closing a probe an immediate
   * stop without a restore.
   */
  @Test
  void runsNormallyAndRecordsNothingWhenNoProbeIsOpen() throws Exception {
    String key = "ProbeFixture.greet.a4";
    InstrumentService.Result result =
        service.instrument(request("greet(String)", key).withArgs(true).withReturn(true));

    Class<?> instrumented = LiveClass.define(CLASS_NAME, result.getBytecode());
    assertEquals(
        "hello world", instrumented.getMethod("greet", String.class).invoke(null, "world"));
  }

  /**
   * The value the method computed is the value the caller gets, by reference. A probe that
   * interned, normalised or copied what it observed would be a probe that changed the program it
   * was measuring.
   */
  @Test
  void handsBackTheSameObjectTheMethodReturned() throws Exception {
    String key = "ProbeFixture.identity.a1";
    Class<?> instrumented = live(request("identity(Object)", key).withReturn(true), key);
    Object fixture = instrumented.getConstructor(String.class).newInstance("p");
    Object token = new Object();

    assertSame(token, instrumented.getMethod("identity", Object.class).invoke(fixture, token));
  }

  @Test
  void refusesAProbeKeyThatCouldNotBeEmbeddedInTheInjectedCode() {
    IllegalArgumentException thrown =
        assertThrows(IllegalArgumentException.class, () -> request("greet", "~compiled/greet"));
    assertTrue(thrown.getMessage().contains("probe key"), () -> thrown.getMessage());
  }
}
