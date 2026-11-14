package com.flanksource.telltale.diag.graph;

import static com.flanksource.telltale.diag.graph.GraphFixtures.activity;
import static com.flanksource.telltale.diag.graph.GraphFixtures.holderWithAMissingFieldType;
import static com.flanksource.telltale.diag.graph.GraphFixtures.policy;
import static com.flanksource.telltale.diag.graph.GraphFixtures.repeat;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.flanksource.telltale.diag.graph.GraphFixtures.Activity;
import com.flanksource.telltale.diag.graph.GraphFixtures.Cycle;
import com.flanksource.telltale.diag.graph.GraphFixtures.LoudException;
import com.flanksource.telltale.diag.graph.GraphFixtures.PersistentBag;
import com.flanksource.telltale.diag.graph.GraphFixtures.Policy;
import com.flanksource.telltale.diag.graph.GraphFixtures.Sequence;
import com.flanksource.telltale.diag.graph.GraphFixtures.Status;
import java.nio.CharBuffer;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.Hashtable;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Stack;
import java.util.Vector;
import java.util.concurrent.Callable;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.LongAccumulator;
import java.util.function.LongBinaryOperator;
import org.junit.jupiter.api.Test;

/**
 * The walk both the OGNL surface and a probe snapshot use. The OGNL half must keep producing what
 * the surface always did; the probe half must never run application code, because it runs on
 * application's own threads.
 */
final class ValueGraphTest {

  private static final int DEPTH = 3;
  private static final int CHILDREN = 100;
  private static final int NODES = 2000;
  private static final int CHARS = 16384;
  private static final int EVENT_CHARS = 262144;

  // ------------------------------------------------------------------ OGNL

  @Test
  void ognlSerializesMapsAndListsWithStableOgnlPaths() {
    Map<String, Object> value = new LinkedHashMap<>();
    value.put("names", Arrays.asList("alpha", "beta"));

    Map<String, Object> tree = ognl(value, 3);

    assertEquals("map", tree.get("kind"));
    Map<String, Object> names = children(tree).get(0);
    assertEquals(".get(\"names\")", names.get("path"));
    assertEquals("list", names.get("kind"));
    assertEquals("names [2]", names.get("label"));
  }

  @Test
  void ognlMarksCyclesAsExpandableIdentityNodes() {
    Cycle value = new Cycle();
    value.self = value;

    Map<String, Object> self = child(ognl(value, 3), "self");

    assertEquals("identity", self.get("kind"));
    assertEquals(Boolean.TRUE, self.get("expandable"));
  }

  /**
   * Only the ancestors are remembered, so an object met twice on different branches is shown in
   * full both times.
   */
  @Test
  void ognlShowsASharedReferenceInFullWhereverItAppears() {
    Policy shared = policy("P-1");
    Map<String, Object> value = new LinkedHashMap<>();
    value.put("left", shared);
    value.put("right", shared);

    Map<String, Object> tree = ognl(value, 3);

    assertEquals("bean", children(tree).get(0).get("kind"));
    assertEquals("bean", children(tree).get(1).get("kind"));
  }

  @Test
  void ognlLeavesAMapPastItsDepthExpandableWithItsSize() {
    Map<String, Object> inner = new LinkedHashMap<>();
    inner.put("a", 1);
    Map<String, Object> value = new LinkedHashMap<>();
    value.put("inner", inner);

    Map<String, Object> cut = child(ognl(value, 1), "inner");

    assertEquals(Boolean.TRUE, cut.get("expandable"));
    assertEquals("size=1", cut.get("raw"));
    assertNull(cut.get("children"));
  }

  // ----------------------------------------------------------------- probe

  /**
   * The failure this replaces: every DCL rendered as {@code ActivityDcl@d7c405bf}, because none
   * overrides toString.
   */
  @Test
  void probeShowsABeansFieldsWithoutCallingItsGettersOrToString() {
    Activity activity = activity();

    ValueGraph.Node root = probe().root(activity, "arg0", null);

    assertEquals("bean", root.kind);
    assertEquals(Activity.class.getName(), root.type);
    assertEquals("ACT-1", scalar(root, "activityGuid"));
    assertEquals("02", scalar(root, "statusCode"));
    assertEquals("P-1", scalar(named(root, "policy"), "policyNumber"));
    assertEquals(0, activity.calls, "no getter and no toString may run on an application thread");
    assertNull(root.id, "a probe snapshot carries no ids; the Go lowering assigns them");
    assertNull(root.path, "nor OGNL paths: the object is gone before anyone could expand it");
  }

  /**
   * A Hibernate collection loads itself on first iteration, firing a query inside the method being
   * measured. Walked by its fields instead, it says whether it was initialised and never loads.
   */
  @Test
  void probeNeverIteratesACollectionItDoesNotKnow() {
    PersistentBag bag = new PersistentBag();

    ValueGraph.Node root = probe().root(bag, "arg0", null);
    probe().root(Collections.unmodifiableList(bag), "arg0", null);

    assertEquals("bean", root.kind);
    assertEquals("false", scalar(root, "initialized"));
    assertEquals(0, bag.touched, "neither the bag nor a JDK wrapper around it may be iterated");
  }

  @Test
  void probeWalksAJdkListOfRowsWithItsSizeInTheLabel() {
    List<Object[]> rows = new ArrayList<>();
    rows.add(new Object[] {"ACT-1", 1});
    rows.add(new Object[] {"ACT-2", 2});

    ValueGraph.Node root = probe().root(rows, "arg0", null);

    assertEquals("arg0 [2]", root.label);
    assertEquals("list", root.kind);
    ValueGraph.Node first = root.children.get(0);
    assertEquals("0 [2]", first.label);
    assertEquals("ACT-1", first.children.get(0).value);
  }

  /** A statement cut at the head alone loses its WHERE clause, which is the part being read. */
  @Test
  void probeKeepsTheHeadAndTheTailOfALongString() {
    String sql =
        "SELECT a.ActivityGUID FROM AsActivity a "
            + repeat('x', 1500)
            + " WHERE a.ActivityGUID NOT IN ('a','b')";
    ValueGraph graph = probe(DEPTH, CHILDREN, NODES, 200, EVENT_CHARS);

    String kept = graph.root(sql, "return", null).value;

    assertTrue(kept.startsWith("SELECT a.ActivityGUID"), kept);
    assertTrue(kept.endsWith("NOT IN ('a','b')"), kept);
    assertTrue(kept.contains("…(+" + (sql.length() - 200) + " chars)…"), kept);
    assertTrue(graph.truncated());
  }

  @Test
  void probeSharesOneTextBudgetAcrossAWholeEvent() {
    ValueGraph graph = probe(DEPTH, CHILDREN, NODES, 80, 100);

    String first = graph.root(repeat('a', 80), "arg0", null).value;
    String second = graph.root(repeat('b', 80), "arg1", null).value;
    String third = graph.root("c", "arg2", null).value;

    assertEquals(repeat('a', 80), first);
    assertEquals(repeat('b', 10) + "…(+60 chars)…" + repeat('b', 10), second);
    assertEquals("…(1 chars not kept: this event's text budget is spent)", third);
  }

  @Test
  void probeStopsExpandingPastTheDepthAndSaysSo() {
    ValueGraph.Node root =
        probe(2, CHILDREN, NODES, CHARS, EVENT_CHARS).root(activity(), "arg0", null);

    ValueGraph.Node plan = named(named(root, "policy"), "plan");

    assertEquals("opaque", plan.kind);
    assertFalse(plan.expandable, "nothing can expand a probe's node later");
    assertTrue(plan.raw.contains("expand"), plan.raw);
  }

  /**
   * A cut at the depth hides fields as surely as one at the child cap, so the event says it was
   * cut.
   */
  @Test
  void probeSaysAnEventWasCutWhenTheDepthHidAnything() {
    ValueGraph beanCut = probe(1, CHILDREN, NODES, CHARS, EVENT_CHARS);
    ValueGraph listCut = probe(1, CHILDREN, NODES, CHARS, EVENT_CHARS);
    ValueGraph nothingHidden = probe(1, CHILDREN, NODES, CHARS, EVENT_CHARS);

    beanCut.root(activity(), "arg0", null);
    listCut.root(Collections.singletonList(Arrays.asList(1, 2)), "arg0", null);
    nothingHidden.root(Collections.singletonList(new ArrayList<Object>()), "arg0", null);

    assertTrue(beanCut.truncated(), "the activity's policy was past the depth");
    assertTrue(listCut.truncated(), "the inner list's two values were past the depth");
    assertFalse(nothingHidden.truncated(), "an empty list past the depth hides nothing");
  }

  /**
   * The first place a walk meets an object can be past the depth, where none of it is shown.
   * Pointing a later, shallower occurrence back at that cut would lose the object from the capture
   * entirely.
   */
  @Test
  void probeShowsAnObjectInFullAtTheFirstPlaceTheDepthAllows() {
    Activity activity = activity();
    ValueGraph graph = probe(2, CHILDREN, NODES, CHARS, EVENT_CHARS);

    ValueGraph.Node cut = named(named(graph.root(activity, "arg0", null), "policy"), "plan");
    ValueGraph.Node whole = graph.root(activity.policy.plan, "arg1", null);

    assertEquals("opaque", cut.kind);
    assertEquals("bean", whole.kind);
    assertEquals("Preservation", scalar(whole, "name"));
  }

  /**
   * Every method of a Vector, a Hashtable and a StringBuffer takes its monitor, and another thread
   * can hold that monitor while it runs foreign code — {@code Vector.indexOf} calls {@code equals},
   * {@code StringBuffer.append} calls {@code toString} — that waits on a lock application's thread
   * holds. Read through their fields, they are shown without taking it.
   */
  @Test
  void probeReadsASynchronizedJdkValueWithoutTakingItsMonitor() throws Exception {
    Hashtable<String, String> table = new Hashtable<>();
    table.put("k", "v");
    Object[] synchronizedValues = {
      new Vector<>(Arrays.asList("a")), new Stack<String>(), table, new StringBuffer("SELECT 1")
    };

    for (Object value : synchronizedValues) {
      assertEquals("bean", walkWhileAnotherThreadHolds(value).kind, value.getClass().getName());
    }
  }

  /** A class under java. is not enough: some render by running what the application handed them. */
  @Test
  void probeNeverRendersAJdkValueThatRunsApplicationCode() {
    Sequence sequence = new Sequence();
    CharBuffer wrapped = CharBuffer.wrap(sequence);
    sequence.calls = 0;
    LongAccumulator accumulator =
        new LongAccumulator(
            new LongBinaryOperator() {
              @Override
              public long applyAsLong(long left, long right) {
                return left + right;
              }
            },
            0L);
    ValueGraph graph = probe();

    ValueGraph.Node buffer = graph.root(wrapped, "arg0", null);
    ValueGraph.Node accumulated = graph.root(accumulator, "arg1", null);

    assertEquals(0, sequence.calls, "the wrapped CharSequence is application code");
    assertEquals("bean", buffer.kind);
    assertEquals(
        "bean", accumulated.kind, "LongAccumulator.toString() applies the caller's function");
  }

  /**
   * A trail is built from labels, and a map key's label can be a whole string: pointing at it is
   * text like any other.
   */
  @Test
  void probeCapsThePointerToAnObjectItAlreadyShowed() {
    Policy policy = policy("P-1");
    Map<String, Object> byKey = new LinkedHashMap<>();
    byKey.put(repeat('k', 500), policy);
    ValueGraph graph = probe(DEPTH, CHILDREN, NODES, 100, EVENT_CHARS);

    graph.root(byKey, "arg0", null);
    String pointer = graph.root(policy, "arg1", null).raw;

    assertTrue(pointer.startsWith("same object as arg0.kkk"), pointer);
    assertTrue(pointer.length() < 120, pointer.length() + ": " + pointer);
  }

  @Test
  void probeCapsTheChildrenOfOneContainerAndCountsTheRest() {
    List<Integer> numbers = new ArrayList<>();
    for (int i = 0; i < 150; i++) {
      numbers.add(i);
    }
    ValueGraph graph = probe();

    ValueGraph.Node root = graph.root(numbers, "arg0", null);

    assertEquals(CHILDREN + 1, root.children.size());
    ValueGraph.Node marker = root.children.get(CHILDREN);
    assertEquals("(truncated)", marker.label);
    assertEquals("50 more values", marker.value);
    assertTrue(graph.truncated());
  }

  @Test
  void probeStopsAtTheNodeBudgetOfTheWholeEvent() {
    List<Integer> numbers = new ArrayList<>();
    for (int i = 0; i < 50; i++) {
      numbers.add(i);
    }
    ValueGraph graph = probe(DEPTH, CHILDREN, 10, CHARS, EVENT_CHARS);

    ValueGraph.Node root = graph.root(numbers, "arg0", null);
    ValueGraph.Node next = graph.root(Arrays.asList(1, 2), "arg1", null);

    assertTrue(root.children.size() <= 10, "kept " + root.children.size());
    assertEquals("(truncated)", root.children.get(root.children.size() - 1).label);
    assertEquals(
        "(truncated)", next.children.get(0).label, "the budget is shared with the next argument");
    assertTrue(graph.truncated());
  }

  /**
   * One DCL passed twice, or reachable from itself, is shown once and then pointed at by the path
   * it was shown at.
   */
  @Test
  void probePointsAtAnObjectItAlreadyShowed() {
    Policy policy = policy("P-1");
    Activity activity = activity();
    activity.policy = policy;
    Cycle cycle = new Cycle();
    cycle.self = cycle;
    ValueGraph graph = probe();

    graph.root(policy, "arg0", null);
    ValueGraph.Node second = graph.root(activity, "arg1", null);
    ValueGraph.Node looped = graph.root(cycle, "arg2", null);

    assertEquals("identity", named(second, "policy").kind);
    assertEquals("same object as arg0", named(second, "policy").raw);
    assertEquals("same object as arg2", named(looped, "self").raw);
  }

  @Test
  void probeRendersEnumsByNameAndExceptionsWithoutTheirOverrides() {
    ValueGraph graph = probe();
    LoudException loud = new LoudException("kept");

    assertEquals("PENDING", graph.root(Status.PENDING, "arg0", null).value);
    assertEquals(
        "java.lang.IllegalStateException: boom",
        graph.root(new IllegalStateException("boom"), "arg1", null).value);
    String rendered = graph.root(loud, "arg2", null).value;
    assertTrue(rendered.startsWith(LoudException.class.getName()), rendered);
    assertFalse(rendered.contains("overridden"), rendered);
    assertEquals(0, loud.calls, "a subclass's getMessage() is application code");
  }

  /**
   * A field whose type cannot be loaded fails getDeclaredFields(); the snapshot says so instead of
   * failing.
   */
  @Test
  void probeReportsABeanWhoseFieldsCannotBeResolved() throws Exception {
    Object holder = holderWithAMissingFieldType();

    ValueGraph.Node root = probe().root(holder, "arg0", null);

    assertEquals("error", root.kind);
    assertEquals("fields unreadable: java.lang.NoClassDefFoundError", root.raw);
  }

  @Test
  void probeChargesWhatItKeeps() {
    ValueGraph small = probe();
    small.root("a", "arg0", null);
    ValueGraph large = probe();
    large.root(repeat('a', 1000), "arg0", null);

    assertTrue(small.bytes() > 0);
    assertTrue(large.bytes() >= small.bytes() + 2 * 999, "strings are charged at two bytes a char");
  }

  @Test
  void recordsAValueAsItsClassAndIdentityOnly() {
    Activity activity = activity();

    ValueGraph.Node node = ValueGraph.opaque("arg0", activity);

    assertEquals("opaque", node.kind);
    assertEquals(Activity.class.getName(), node.type);
    assertTrue(node.raw.startsWith(Activity.class.getName() + "@"), node.raw);
    assertNull(node.children);
    assertEquals(0, activity.calls);
  }

  // --------------------------------------------------------------- helpers

  private static ValueGraph probe() {
    return probe(DEPTH, CHILDREN, NODES, CHARS, EVENT_CHARS);
  }

  /**
   * Walks {@code monitor} while another thread holds it; a walk that waits for the monitor times
   * out and fails.
   */
  private static ValueGraph.Node walkWhileAnotherThreadHolds(final Object monitor)
      throws Exception {
    final CountDownLatch held = new CountDownLatch(1);
    final CountDownLatch release = new CountDownLatch(1);
    Thread holder =
        new Thread(
            new Runnable() {
              @Override
              public void run() {
                synchronized (monitor) {
                  held.countDown();
                  try {
                    release.await();
                  } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                  }
                }
              }
            });
    holder.start();
    held.await();
    ExecutorService walker = Executors.newSingleThreadExecutor();
    try {
      return walker
          .submit(
              new Callable<ValueGraph.Node>() {
                @Override
                public ValueGraph.Node call() {
                  return probe().root(monitor, "arg0", null);
                }
              })
          .get(5, TimeUnit.SECONDS);
    } finally {
      release.countDown();
      walker.shutdownNow();
      holder.join();
    }
  }

  private static ValueGraph probe(int depth, int children, int nodes, int chars, int eventChars) {
    return new ValueGraph(GraphLimits.probe(depth, children, nodes, chars, eventChars));
  }

  private static Map<String, Object> ognl(Object value, int depth) {
    return new ValueGraph(GraphLimits.ognl(depth)).root(value, "value", "").toMap();
  }

  @SuppressWarnings("unchecked")
  private static List<Map<String, Object>> children(Map<String, Object> node) {
    return (List<Map<String, Object>>) node.get("children");
  }

  private static Map<String, Object> child(Map<String, Object> node, String label) {
    for (Map<String, Object> child : children(node)) {
      if (label.equals(child.get("label"))) {
        return child;
      }
    }
    throw new AssertionError("no child " + label + " in " + node);
  }

  private static ValueGraph.Node named(ValueGraph.Node node, String label) {
    assertNotNull(node.children, "no children under " + node.label);
    for (ValueGraph.Node child : node.children) {
      if (label.equals(child.label)) {
        return child;
      }
    }
    throw new AssertionError("no child " + label + " under " + node.label);
  }

  private static String scalar(ValueGraph.Node node, String label) {
    return named(node, label).value;
  }
}
