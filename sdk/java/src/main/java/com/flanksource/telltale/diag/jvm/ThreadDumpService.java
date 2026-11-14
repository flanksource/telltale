package com.flanksource.telltale.diag.jvm;

import java.lang.management.LockInfo;
import java.lang.management.ManagementFactory;
import java.lang.management.MonitorInfo;
import java.lang.management.ThreadInfo;
import java.lang.management.ThreadMXBean;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.NoSuchElementException;
import java.util.Set;

/**
 * Read-only thread diagnostics over {@link ThreadMXBean}: a CPU-sampled thread table, one thread's
 * full stack, and a jstack-style dump of every thread.
 *
 * <p>Nothing is switched on to answer: thread contention monitoring stays as the JVM has it, so
 * blocked/waited times are {@code null} when it is off rather than a {@code 0} that reads as "never
 * waited". The same holds for CPU time.
 *
 * <p>The text is formatted here rather than by {@link ThreadInfo#toString()}, which stops after
 * eight frames — the frames below them are usually the ones that say which application call the
 * thread is in.
 */
public final class ThreadDumpService {

  public static final int DEFAULT_SAMPLE_MS = 200;
  public static final int MIN_SAMPLE_MS = 50;
  public static final int MAX_SAMPLE_MS = 2000;

  private static final long NANOS_PER_MS = 1_000_000L;

  private final ThreadMXBean threads;

  public ThreadDumpService() {
    this(ManagementFactory.getThreadMXBean());
  }

  ThreadDumpService(ThreadMXBean threads) {
    this.threads = threads;
  }

  /**
   * Every live thread, with CPU % measured as the thread CPU time it used over {@code sampleMs} of
   * wall clock. The request blocks for that window.
   */
  public Map<String, Object> snapshot(int sampleMs) throws InterruptedException {
    if (sampleMs < MIN_SAMPLE_MS || sampleMs > MAX_SAMPLE_MS) {
      throw new IllegalArgumentException(
          "sampleMs must be between "
              + MIN_SAMPLE_MS
              + " and "
              + MAX_SAMPLE_MS
              + ", got "
              + sampleMs);
    }
    boolean cpuEnabled = cpuTimeEnabled();
    Map<Long, Long> before = cpuEnabled ? cpuTimes(threads.getAllThreadIds()) : null;
    long startedAt = System.nanoTime();
    Thread.sleep(sampleMs);
    long[] ids = threads.getAllThreadIds();
    Map<Long, Long> after = cpuEnabled ? cpuTimes(ids) : null;
    long elapsed = System.nanoTime() - startedAt;

    ThreadInfo[] infos = threads.getThreadInfo(ids);
    Map<Long, Thread> live = liveThreads();
    List<Map<String, Object>> rows = new ArrayList<>();
    for (ThreadInfo info : infos) {
      // Null, or absent from the live set, is a thread that ended during the
      // read: it no longer exists to be diagnosed.
      if (info == null || !live.containsKey(info.getThreadId())) continue;
      long id = info.getThreadId();
      Double cpuPercent = null;
      Long cpuTimeMs = null;
      if (cpuEnabled) {
        Long used = after.get(id);
        if (used == null) continue;
        // A thread started inside the window had used no CPU before it.
        Long base = before.containsKey(id) ? before.get(id) : 0L;
        cpuPercent = Math.round((used - base) * 1000D / elapsed) / 10D;
        cpuTimeMs = used / NANOS_PER_MS;
      }
      rows.add(row(info, live.get(id), cpuPercent, cpuTimeMs));
    }

    Map<String, Object> out = new LinkedHashMap<>();
    out.put("sampledMs", sampleMs);
    out.put("cpuTimeEnabled", cpuEnabled);
    out.put("deadlocked", deadlocked());
    out.put("threads", rows);
    return out;
  }

  /** One thread's row, its jstack-format text, and its frames, at full depth. */
  public Map<String, Object> stack(long id) {
    ThreadInfo info =
        threads
            .getThreadInfo(
                new long[] {id},
                threads.isObjectMonitorUsageSupported(),
                threads.isSynchronizerUsageSupported())[0];
    Thread thread = liveThreads().get(id);
    if (info == null || thread == null) {
      throw new NoSuchElementException("thread " + id + " is not alive in this JVM");
    }
    Long cpuTimeMs = null;
    if (cpuTimeEnabled()) {
      long used = threads.getThreadCpuTime(id);
      if (used < 0)
        throw new NoSuchElementException("thread " + id + " ended while it was being read");
      cpuTimeMs = used / NANOS_PER_MS;
    }
    List<Map<String, Object>> frames = new ArrayList<>();
    for (StackTraceElement element : info.getStackTrace()) {
      Map<String, Object> frame = new LinkedHashMap<>();
      frame.put("className", element.getClassName());
      frame.put("methodName", element.getMethodName());
      frame.put("fileName", element.getFileName());
      frame.put("lineNumber", element.getLineNumber());
      frames.add(frame);
    }
    Map<String, Object> out = new LinkedHashMap<>();
    out.put("thread", row(info, thread, null, cpuTimeMs));
    out.put("text", format(info, thread));
    out.put("frames", frames);
    return out;
  }

  /** Every thread in jstack format, then the deadlocks, if any. */
  public String dump() {
    ThreadInfo[] infos =
        threads.dumpAllThreads(
            threads.isObjectMonitorUsageSupported(), threads.isSynchronizerUsageSupported());
    Map<Long, Thread> live = liveThreads();
    StringBuilder out = new StringBuilder();
    Map<Long, ThreadInfo> byId = new HashMap<>();
    for (ThreadInfo info : infos) {
      if (info == null) continue;
      byId.put(info.getThreadId(), info);
      out.append(format(info, live.get(info.getThreadId()))).append('\n');
    }
    out.append(deadlockReport(deadlocked(), byId));
    return out.toString();
  }

  private boolean cpuTimeEnabled() {
    return threads.isThreadCpuTimeSupported() && threads.isThreadCpuTimeEnabled();
  }

  /** Thread id to CPU nanoseconds, leaving out threads that ended (-1). */
  private Map<Long, Long> cpuTimes(long[] ids) {
    Map<Long, Long> out = new HashMap<>();
    for (long id : ids) {
      long used = threads.getThreadCpuTime(id);
      if (used >= 0) out.put(id, used);
    }
    return out;
  }

  /**
   * Live {@link Thread}s by id: {@code ThreadInfo.isDaemon()} and {@code getPriority()} are Java
   * 9+, and this WAR runs on Java 8.
   */
  private static Map<Long, Thread> liveThreads() {
    Map<Long, Thread> out = new HashMap<>();
    for (Thread thread : Thread.getAllStackTraces().keySet()) {
      out.put(thread.getId(), thread);
    }
    return out;
  }

  private List<Long> deadlocked() {
    long[] ids = threads.findDeadlockedThreads();
    if (ids == null) return Collections.emptyList();
    List<Long> out = new ArrayList<>();
    for (long id : ids) out.add(id);
    return out;
  }

  private Map<String, Object> row(
      ThreadInfo info, Thread thread, Double cpuPercent, Long cpuTimeMs) {
    boolean contention =
        threads.isThreadContentionMonitoringSupported()
            && threads.isThreadContentionMonitoringEnabled();
    Map<String, Object> row = new LinkedHashMap<>();
    row.put("id", info.getThreadId());
    row.put("name", info.getThreadName());
    row.put("state", info.getThreadState().name());
    row.put("daemon", thread.isDaemon());
    row.put("priority", thread.getPriority());
    row.put("cpuPercent", cpuPercent);
    row.put("cpuTimeMs", cpuTimeMs);
    row.put("blockedCount", info.getBlockedCount());
    row.put("blockedTimeMs", contention ? info.getBlockedTime() : null);
    row.put("waitedCount", info.getWaitedCount());
    row.put("waitedTimeMs", contention ? info.getWaitedTime() : null);
    row.put("lockName", info.getLockName());
    row.put("lockOwnerId", info.getLockOwnerId() < 0 ? null : info.getLockOwnerId());
    row.put("lockOwnerName", info.getLockOwnerName());
    row.put("inNative", info.isInNative());
    row.put("suspended", info.isSuspended());
    return row;
  }

  /** One thread in jstack format; {@code thread} is null when it ended mid-dump. */
  static String format(ThreadInfo info, Thread thread) {
    StringBuilder out = new StringBuilder();
    out.append('"').append(info.getThreadName()).append("\" #").append(info.getThreadId());
    if (thread != null) {
      if (thread.isDaemon()) out.append(" daemon");
      out.append(" prio=").append(thread.getPriority());
    }
    out.append('\n');
    out.append("   java.lang.Thread.State: ").append(info.getThreadState()).append('\n');
    StackTraceElement[] stack = info.getStackTrace();
    MonitorInfo[] monitors = info.getLockedMonitors();
    for (int depth = 0; depth < stack.length; depth++) {
      out.append("\tat ").append(frame(stack[depth])).append('\n');
      if (depth == 0) appendBlockedOn(out, info, stack[0]);
      for (MonitorInfo monitor : monitors) {
        if (monitor.getLockedStackDepth() == depth) {
          out.append("\t- locked ").append(lock(monitor)).append('\n');
        }
      }
    }
    out.append("\n   Locked ownable synchronizers:\n");
    LockInfo[] synchronizers = info.getLockedSynchronizers();
    if (synchronizers.length == 0) {
      out.append("\t- None\n");
    }
    for (LockInfo synchronizer : synchronizers) {
      out.append("\t- ").append(lock(synchronizer)).append('\n');
    }
    return out.toString();
  }

  /** The jstack line under frame 0 naming what the thread is stopped on. */
  private static void appendBlockedOn(StringBuilder out, ThreadInfo info, StackTraceElement top) {
    LockInfo lock = info.getLockInfo();
    if (lock == null) return;
    String verb;
    switch (info.getThreadState()) {
      case BLOCKED:
        verb = "waiting to lock ";
        break;
      case WAITING:
      case TIMED_WAITING:
        boolean parked =
            top.getClassName().endsWith(".Unsafe") && "park".equals(top.getMethodName());
        verb = parked ? "parking to wait for " : "waiting on ";
        break;
      default:
        return;
    }
    out.append("\t- ").append(verb).append(lock(lock));
    if (info.getLockOwnerName() != null) {
      out.append(" owned by \"")
          .append(info.getLockOwnerName())
          .append("\" Id=")
          .append(info.getLockOwnerId());
    }
    out.append('\n');
  }

  private static String frame(StackTraceElement element) {
    String location;
    if (element.isNativeMethod()) {
      location = "Native Method";
    } else if (element.getFileName() == null) {
      location = "Unknown Source";
    } else if (element.getLineNumber() >= 0) {
      location = element.getFileName() + ":" + element.getLineNumber();
    } else {
      location = element.getFileName();
    }
    return element.getClassName() + "." + element.getMethodName() + "(" + location + ")";
  }

  private static String lock(LockInfo lock) {
    return String.format("<0x%08x> (a %s)", lock.getIdentityHashCode(), lock.getClassName());
  }

  /**
   * jstack's trailer: each deadlock cycle, as who waits for what and who holds it. {@code
   * findDeadlockedThreads} returns the threads, not the cycles, so the cycles are recovered by
   * following lock owners.
   */
  private static String deadlockReport(List<Long> deadlocked, Map<Long, ThreadInfo> byId) {
    if (deadlocked.isEmpty()) return "";
    List<List<ThreadInfo>> cycles = deadlockCycles(deadlocked, byId);
    StringBuilder out = new StringBuilder();
    out.append("Found ")
        .append(cycles.size())
        .append(cycles.size() == 1 ? " deadlock:" : " deadlocks:")
        .append("\n");
    for (List<ThreadInfo> cycle : cycles) {
      out.append('\n');
      for (ThreadInfo info : cycle) {
        out.append('"')
            .append(info.getThreadName())
            .append("\" Id=")
            .append(info.getThreadId())
            .append(":\n  waiting for ")
            .append(lock(info.getLockInfo()))
            .append(",\n  which is held by \"")
            .append(info.getLockOwnerName())
            .append("\" Id=")
            .append(info.getLockOwnerId())
            .append('\n');
      }
    }
    return out.toString();
  }

  private static List<List<ThreadInfo>> deadlockCycles(
      List<Long> deadlocked, Map<Long, ThreadInfo> byId) {
    Set<Long> visited = new HashSet<>();
    List<List<ThreadInfo>> cycles = new ArrayList<>();
    for (Long start : deadlocked) {
      LinkedHashSet<Long> path = new LinkedHashSet<>();
      Long current = start;
      while (current != null && !visited.contains(current) && byId.containsKey(current)) {
        path.add(current);
        visited.add(current);
        long owner = byId.get(current).getLockOwnerId();
        current = owner < 0 ? null : owner;
      }
      if (current == null || !path.contains(current)) continue;
      List<ThreadInfo> cycle = new ArrayList<>();
      boolean inCycle = false;
      for (Long id : path) {
        inCycle = inCycle || id.equals(current);
        if (inCycle) cycle.add(byId.get(id));
      }
      cycles.add(cycle);
    }
    return cycles;
  }
}
