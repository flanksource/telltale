package com.flanksource.telltale.diag.jvm;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.lang.management.ManagementFactory;
import java.lang.management.ThreadMXBean;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Proxy;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Map;
import java.util.NoSuchElementException;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import org.junit.jupiter.api.Test;

final class ThreadDumpServiceTest {

  private static final int SAMPLE_MS = 200;
  private static final int RECURSION_DEPTH = 20;
  private static final long DEADLOCK_WAIT_MS = 10_000;

  private final ThreadDumpService service = new ThreadDumpService();

  @Test
  void snapshotSamplesPerThreadCpuOverTheRequestedWindow() throws Exception {
    AtomicBoolean spinning = new AtomicBoolean(true);
    Thread spinner =
        daemon(
            "threads-test-spinner",
            () -> {
              long sink = 0;
              while (spinning.get()) {
                sink += System.nanoTime() % 7;
              }
              if (sink == Long.MIN_VALUE) {
                throw new IllegalStateException("unreachable");
              }
            });
    spinner.start();
    try {
      Map<String, Object> snapshot = service.snapshot(SAMPLE_MS);

      assertEquals(SAMPLE_MS, snapshot.get("sampledMs"));
      assertEquals(Boolean.TRUE, snapshot.get("cpuTimeEnabled"));
      assertInstanceOf(List.class, snapshot.get("deadlocked"));
      Map<String, Object> row = row(snapshot, spinner.getId());
      assertEquals("threads-test-spinner", row.get("name"));
      assertEquals(Boolean.TRUE, row.get("daemon"));
      assertEquals(Thread.NORM_PRIORITY, row.get("priority"));
      assertInstanceOf(Double.class, row.get("cpuPercent"));
      assertTrue((Double) row.get("cpuPercent") > 0D, "a spinning thread used no CPU: " + row);
      assertInstanceOf(Long.class, row.get("cpuTimeMs"));
      assertTrue(
          row.containsKey("lockOwnerId") && row.get("lockOwnerId") == null,
          "no lock owner: " + row);
      assertContentionTimes(row);
    } finally {
      spinning.set(false);
      spinner.join();
    }
  }

  @Test
  void snapshotReportsCpuAsUnknownWhenThreadCpuTimeIsDisabled() throws Exception {
    Map<String, Object> snapshot = new ThreadDumpService(cpuTimeDisabled()).snapshot(50);

    assertEquals(Boolean.FALSE, snapshot.get("cpuTimeEnabled"));
    Map<String, Object> row = row(snapshot, Thread.currentThread().getId());
    assertTrue(
        row.containsKey("cpuPercent") && row.get("cpuPercent") == null, "cpuPercent: " + row);
    assertTrue(row.containsKey("cpuTimeMs") && row.get("cpuTimeMs") == null, "cpuTimeMs: " + row);
  }

  @Test
  void rejectsASampleWindowOutsideTheContract() {
    assertThrows(IllegalArgumentException.class, () -> service.snapshot(49));
    assertThrows(IllegalArgumentException.class, () -> service.snapshot(2001));
  }

  @Test
  void reportsAnUnknownThreadAsMissing() {
    assertThrows(NoSuchElementException.class, () -> service.stack(Long.MAX_VALUE));
  }

  @Test
  void stackRendersEveryFrameBeyondThreadInfoToStringsEightFrameCut() throws Exception {
    CountDownLatch parked = new CountDownLatch(1);
    CountDownLatch release = new CountDownLatch(1);
    Thread deep = daemon("threads-test-deep", () -> descend(RECURSION_DEPTH, parked, release));
    deep.start();
    try {
      assertTrue(
          parked.await(DEADLOCK_WAIT_MS, TimeUnit.MILLISECONDS), "recursion never bottomed out");
      long deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(DEADLOCK_WAIT_MS);
      while (deep.getState() != Thread.State.WAITING && System.nanoTime() < deadline) {
        Thread.sleep(1);
      }
      assertEquals(Thread.State.WAITING, deep.getState(), "deep thread did not park");
      Map<String, Object> stack = service.stack(deep.getId());

      String text = (String) stack.get("text");
      assertTrue(
          text.startsWith("\"threads-test-deep\" #" + deep.getId() + " daemon prio=5\n"), text);
      assertTrue(text.contains("   java.lang.Thread.State: WAITING"), text);
      assertEquals(RECURSION_DEPTH + 1, count(text, ".descend(ThreadDumpServiceTest.java:"), text);
      assertTrue(text.contains("\n   Locked ownable synchronizers:\n\t- None\n"), text);
      @SuppressWarnings("unchecked")
      List<Map<String, Object>> frames = (List<Map<String, Object>>) stack.get("frames");
      long descents = frames.stream().filter(f -> "descend".equals(f.get("methodName"))).count();
      assertEquals(RECURSION_DEPTH + 1, descents);
      Map<String, Object> top =
          frames.stream()
              .filter(f -> "descend".equals(f.get("methodName")))
              .findFirst()
              .orElseThrow(IllegalStateException::new);
      assertEquals(ThreadDumpServiceTest.class.getName(), top.get("className"));
      assertEquals("ThreadDumpServiceTest.java", top.get("fileName"));
      assertInstanceOf(Integer.class, top.get("lineNumber"));
      @SuppressWarnings("unchecked")
      Map<String, Object> thread = (Map<String, Object>) stack.get("thread");
      assertNull(thread.get("cpuPercent"));
      assertInstanceOf(Long.class, thread.get("cpuTimeMs"));
    } finally {
      release.countDown();
      deep.join();
    }
  }

  /**
   * A monitor deadlock cannot be broken from outside: BLOCKED on monitor entry ignores interrupts.
   * The two threads are daemons with names no other test uses, so they idle until this test JVM
   * exits without holding it open.
   */
  @Test
  void deadlockedMonitorsAppearInTheSnapshotStackAndDump() throws Exception {
    Object first = new Object();
    Object second = new Object();
    CountDownLatch bothHoldTheirFirst = new CountDownLatch(2);
    Thread a = daemon("threads-test-deadlock-a", () -> lockBoth(first, second, bothHoldTheirFirst));
    Thread b = daemon("threads-test-deadlock-b", () -> lockBoth(second, first, bothHoldTheirFirst));
    a.start();
    b.start();
    awaitDeadlock(a.getId(), b.getId());

    Map<String, Object> snapshot = service.snapshot(50);
    @SuppressWarnings("unchecked")
    List<Long> deadlocked = (List<Long>) snapshot.get("deadlocked");
    assertTrue(
        deadlocked.containsAll(Arrays.asList(a.getId(), b.getId())), "deadlocked: " + deadlocked);
    Map<String, Object> blocked = row(snapshot, a.getId());
    assertEquals("BLOCKED", blocked.get("state"));
    assertEquals(b.getId(), blocked.get("lockOwnerId"));
    assertEquals("threads-test-deadlock-b", blocked.get("lockOwnerName"));
    assertNotNull(blocked.get("lockName"));

    String text = (String) service.stack(a.getId()).get("text");
    assertTrue(text.contains("   java.lang.Thread.State: BLOCKED\n"), text);
    assertTrue(
        text.matches(
            "(?s).*\t- waiting to lock <0x[0-9a-f]{8}> \\(a java\\.lang\\.Object\\)"
                + " owned by \"threads-test-deadlock-b\" Id="
                + b.getId()
                + "\n.*"),
        text);
    assertTrue(
        text.matches("(?s).*\t- locked <0x[0-9a-f]{8}> \\(a java\\.lang\\.Object\\)\n.*"), text);

    String dump = service.dump();
    assertTrue(dump.contains("\"threads-test-deadlock-a\" #" + a.getId()), dump);
    assertTrue(dump.contains("\"threads-test-deadlock-b\" #" + b.getId()), dump);
    assertTrue(dump.contains("Found 1 deadlock"), dump);
  }

  private static void descend(int remaining, CountDownLatch parked, CountDownLatch release) {
    if (remaining > 0) {
      descend(remaining - 1, parked, release);
      return;
    }
    parked.countDown();
    try {
      release.await();
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
    }
  }

  private static void lockBoth(Object held, Object wanted, CountDownLatch bothHoldTheirFirst) {
    synchronized (held) {
      bothHoldTheirFirst.countDown();
      try {
        bothHoldTheirFirst.await();
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
        return;
      }
      synchronized (wanted) {
        throw new IllegalStateException("the deadlock did not form");
      }
    }
  }

  private static void awaitDeadlock(long... ids) throws InterruptedException {
    ThreadMXBean threads = ManagementFactory.getThreadMXBean();
    long deadline = System.currentTimeMillis() + DEADLOCK_WAIT_MS;
    while (System.currentTimeMillis() < deadline) {
      long[] found = threads.findDeadlockedThreads();
      if (found != null && containsAll(found, ids)) {
        return;
      }
      Thread.sleep(20);
    }
    throw new AssertionError("threads " + Arrays.toString(ids) + " never deadlocked");
  }

  private static boolean containsAll(long[] found, long[] ids) {
    List<Long> boxed = new ArrayList<>();
    for (long id : found) {
      boxed.add(id);
    }
    for (long id : ids) {
      if (!boxed.contains(id)) {
        return false;
      }
    }
    return true;
  }

  private static void assertContentionTimes(Map<String, Object> row) {
    assertInstanceOf(Long.class, row.get("blockedCount"));
    assertInstanceOf(Long.class, row.get("waitedCount"));
    if (ManagementFactory.getThreadMXBean().isThreadContentionMonitoringEnabled()) {
      assertInstanceOf(Long.class, row.get("blockedTimeMs"));
      return;
    }
    assertTrue(
        row.containsKey("blockedTimeMs") && row.get("blockedTimeMs") == null,
        "blockedTimeMs: " + row);
    assertTrue(
        row.containsKey("waitedTimeMs") && row.get("waitedTimeMs") == null, "waitedTimeMs: " + row);
  }

  @SuppressWarnings("unchecked")
  private static Map<String, Object> row(Map<String, Object> snapshot, long id) {
    for (Map<String, Object> row : (List<Map<String, Object>>) snapshot.get("threads")) {
      if (Long.valueOf(id).equals(row.get("id"))) {
        return row;
      }
    }
    throw new AssertionError("thread " + id + " is not in the snapshot");
  }

  private static int count(String text, String needle) {
    int found = 0;
    for (int at = text.indexOf(needle); at >= 0; at = text.indexOf(needle, at + 1)) {
      found++;
    }
    return found;
  }

  private static Thread daemon(String name, Runnable body) {
    Thread thread = new Thread(body, name);
    thread.setDaemon(true);
    return thread;
  }

  /** The platform bean, except it reports thread CPU time as switched off. */
  private static ThreadMXBean cpuTimeDisabled() {
    ThreadMXBean real = ManagementFactory.getThreadMXBean();
    return (ThreadMXBean)
        Proxy.newProxyInstance(
            ThreadMXBean.class.getClassLoader(),
            new Class<?>[] {ThreadMXBean.class},
            (proxy, method, args) -> {
              if ("isThreadCpuTimeEnabled".equals(method.getName())) {
                return false;
              }
              if (method.getName().contains("CpuTime") && !method.getName().startsWith("is")) {
                throw new AssertionError(method.getName() + " read while CPU time is disabled");
              }
              try {
                return method.invoke(real, args);
              } catch (InvocationTargetException e) {
                throw e.getCause();
              }
            });
  }
}
