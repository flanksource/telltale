package com.flanksource.telltale.diag.jmx;

import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Deque;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicLong;
import java.util.logging.Level;
import java.util.logging.Logger;
import javax.management.MBeanServer;
import javax.management.Notification;
import javax.management.NotificationListener;
import javax.management.ObjectName;

/**
 * Subscribes to JMX notifications on arbitrary MBeans and buffers them per subscriber. One shared
 * {@link NotificationListener} is registered per target MBean and fans out to every subscriber of
 * that MBean; subscribers poll their own bounded queue (drained by the SSE servlet).
 */
public final class NotificationHub {

  private static final Logger LOG = Logger.getLogger(NotificationHub.class.getName());

  /** Ring size when a subscriber does not ask for one. */
  public static final int DEFAULT_CAPACITY = 500;

  /** Largest ring a subscriber may ask for; each slot pins one serialized notification. */
  public static final int MAX_CAPACITY = 50_000;

  private final MBeanServer mbs;
  private final ConcurrentHashMap<String, Subscriber> subscribers = new ConcurrentHashMap<>();
  private final ConcurrentHashMap<ObjectName, NotificationListener> listeners =
      new ConcurrentHashMap<>();

  public NotificationHub(MBeanServer mbs) {
    this.mbs = mbs;
  }

  public static final class Subscriber {
    final String id;

    /** What the subscriber asked for: one MBean, or a pattern. */
    final ObjectName target;

    /** The MBeans it listens to: the target itself, or every MBean the pattern matched. */
    final List<ObjectName> sources;

    final List<String> filters;
    final int capacity;
    final BlockingQueue<Map<String, Object>> queue;
    final Deque<Map<String, Object>> history;
    final AtomicLong dropped = new AtomicLong();
    long writeSeq;
    boolean active = true;

    Subscriber(
        String id,
        ObjectName target,
        List<ObjectName> sources,
        List<String> filters,
        int capacity) {
      this.id = id;
      this.target = target;
      this.sources = sources;
      this.filters = filters;
      this.capacity = capacity;
      this.queue = new ArrayBlockingQueue<Map<String, Object>>(capacity);
      this.history = new ArrayDeque<Map<String, Object>>(capacity);
    }

    boolean accepts(Notification n) {
      if (filters == null || filters.isEmpty()) {
        return true;
      }
      String type = n.getType();
      for (String f : filters) {
        if (type != null && (type.equals(f) || type.startsWith(f))) {
          return true;
        }
      }
      return false;
    }

    synchronized void offer(Map<String, Object> event) {
      if (!active) {
        return;
      }
      if (!queue.offer(event)) {
        queue.poll();
        dropped.incrementAndGet();
        queue.offer(event);
      }
      if (history.size() == capacity) {
        history.removeFirst();
      }
      history.addLast(event);
      writeSeq++;
    }

    synchronized Map<String, Object> read(long since, int limit) {
      if (since < 0L) {
        throw new IllegalArgumentException("notification cursor must not be negative: " + since);
      }
      if (limit <= 0) {
        throw new IllegalArgumentException("notification limit must be positive: " + limit);
      }
      if (since > writeSeq) {
        throw new IllegalArgumentException(
            "notification cursor " + since + " is ahead of write cursor " + writeSeq);
      }
      long oldest = writeSeq - history.size();
      long start = Math.max(since, oldest);
      long skip = start - oldest;
      List<Map<String, Object>> events = new ArrayList<Map<String, Object>>();
      int index = 0;
      for (Map<String, Object> event : history) {
        if (index++ < skip) {
          continue;
        }
        if (events.size() == limit) {
          break;
        }
        events.add(event);
      }
      Map<String, Object> batch = new LinkedHashMap<String, Object>();
      batch.put("id", id);
      batch.put("generation", id);
      batch.put("events", events);
      batch.put("nextSeq", Long.valueOf(start + events.size()));
      batch.put("oldestSeq", Long.valueOf(oldest));
      batch.put("writeSeq", Long.valueOf(writeSeq));
      batch.put("dropped", Long.valueOf(Math.max(0L, oldest - since)));
      batch.put("active", Boolean.valueOf(active));
      return batch;
    }

    synchronized boolean freeze() {
      boolean changed = active;
      active = false;
      return changed;
    }

    synchronized boolean isActive() {
      return active;
    }
  }

  /**
   * Register a subscriber on {@code mbeanName} (required). A pattern ({@code
   * java.lang:type=GarbageCollector,*}) subscribes to every MBean it matches at registration.
   * Ensures a shared listener on each source MBean. Throws (fail loud) if the MBean is absent, a
   * pattern matches nothing, a source is not a NotificationBroadcaster, or if {@code capacity} is
   * outside 1..{@link #MAX_CAPACITY}.
   */
  public synchronized String register(String mbeanName, List<String> filters, int capacity)
      throws Exception {
    checkCapacity(capacity);
    if (mbeanName == null || mbeanName.trim().isEmpty())
      throw new IllegalArgumentException("mbean is required");
    ObjectName target = new ObjectName(mbeanName.trim());
    List<ObjectName> sources = sourcesOf(target);
    try {
      for (ObjectName source : sources) {
        ensureListener(source); // throws if not a broadcaster
      }
    } catch (Exception notBroadcaster) {
      // A pattern can fail on its third match; the first two must not keep
      // listening for a subscriber that never existed.
      for (ObjectName source : sources) {
        if (!listenedToBySomeoneElse(source, null)) {
          removeListener(source);
        }
      }
      throw notBroadcaster;
    }
    String id = UUID.randomUUID().toString();
    subscribers.put(id, new Subscriber(id, target, sources, filters, capacity));
    return id;
  }

  /** Takes a long so a JSON number beyond int range is refused rather than wrapped. */
  public static int checkCapacity(long capacity) {
    if (capacity < 1L || capacity > MAX_CAPACITY) {
      throw new IllegalArgumentException(
          "notification capacity must be between 1 and " + MAX_CAPACITY + ", got " + capacity);
    }
    return (int) capacity;
  }

  /**
   * The registered MBeans {@code target} names: itself, or every match of a pattern, in name order.
   */
  private List<ObjectName> sourcesOf(ObjectName target) {
    if (!target.isPattern()) {
      if (!mbs.isRegistered(target)) {
        throw new IllegalArgumentException("MBean not registered: " + target);
      }
      return Collections.singletonList(target);
    }
    List<ObjectName> matched = new ArrayList<ObjectName>(mbs.queryNames(target, null));
    if (matched.isEmpty()) {
      throw new IllegalArgumentException("no MBean matches pattern " + target);
    }
    Collections.sort(matched);
    return matched;
  }

  private void ensureListener(ObjectName target) throws Exception {
    // computeIfAbsent can't throw checked exceptions, so guard explicitly.
    if (listeners.containsKey(target)) {
      return;
    }
    synchronized (listeners) {
      if (listeners.containsKey(target)) {
        return;
      }
      NotificationListener l =
          new NotificationListener() {
            @Override
            public void handleNotification(Notification n, Object handback) {
              fanOut(target, n);
            }
          };
      mbs.addNotificationListener(target, l, null, null); // may throw if not a broadcaster
      listeners.put(target, l);
      LOG.log(Level.INFO, "telltale: listening on {0}", target);
    }
  }

  private void fanOut(ObjectName source, Notification n) {
    Map<String, Object> event = serialize(n);
    for (Subscriber s : subscribers.values()) {
      if (!s.sources.contains(source) || !s.accepts(n)) {
        continue;
      }
      s.offer(event);
    }
  }

  /**
   * One notification as the cursor read serves it. A GC notification carries its decoded collection
   * as {@code gc} in place of the raw user data; one whose user data will not decode keeps the raw
   * data and says why in {@code gcError}, so the failure reaches the reader instead of dying in the
   * listener thread.
   */
  static Map<String, Object> serialize(Notification n) {
    Map<String, Object> m = new LinkedHashMap<>();
    m.put("type", n.getType());
    m.put("sequenceNumber", n.getSequenceNumber());
    m.put("timeStamp", n.getTimeStamp());
    m.put("message", n.getMessage());
    m.put("source", String.valueOf(n.getSource()));
    if (GcNotifications.TYPE.equals(n.getType())) {
      try {
        m.put("gc", GcNotifications.decode(n.getUserData()));
        return m;
      } catch (RuntimeException undecodable) {
        m.put("gcError", String.valueOf(undecodable));
      }
    }
    m.put("userData", JmxService.simplify(n.getUserData()));
    return m;
  }

  /** Block up to {@code timeoutMs} for the next notification for a subscriber. */
  public Map<String, Object> poll(String id, long timeoutMs) throws InterruptedException {
    Subscriber s = subscribers.get(id);
    if (s == null) {
      return null;
    }
    return s.queue.poll(timeoutMs, TimeUnit.MILLISECONDS);
  }

  /**
   * Non-blocking drain of all currently-buffered notifications for a subscriber. Backs the {@code
   * NotificationBuffer} MBean so a Jolokia client can pull notifications via {@code exec drain(id)}
   * — Jolokia 1.x supports {@code exec} even though it cannot subscribe to JMX notifications
   * itself.
   */
  public List<Map<String, Object>> drain(String id) {
    Subscriber s = subscribers.get(id);
    if (s == null) {
      throw new IllegalArgumentException("unknown subscription: " + id);
    }
    List<Map<String, Object>> out = new ArrayList<>();
    s.queue.drainTo(out);
    return out;
  }

  /** Non-destructive cursor read used by durable record-store followers. */
  public Map<String, Object> read(String id, long since, int limit) {
    Subscriber s = subscribers.get(id);
    if (s == null) {
      throw new IllegalArgumentException("unknown subscription: " + id);
    }
    return s.read(since, limit);
  }

  /** Stops new notifications while retaining the subscriber's readable history. */
  public boolean freeze(String id) {
    Subscriber s = subscribers.get(id);
    if (s == null) {
      throw new IllegalArgumentException("unknown subscription: " + id);
    }
    return s.freeze();
  }

  public boolean exists(String id) {
    return subscribers.containsKey(id);
  }

  public boolean active(String id) {
    Subscriber s = subscribers.get(id);
    return s != null && s.isActive();
  }

  public int queued(String id) {
    Subscriber s = subscribers.get(id);
    return s == null ? 0 : s.queue.size();
  }

  public long dropped(String id) {
    Subscriber s = subscribers.get(id);
    return s == null ? 0 : s.dropped.get();
  }

  public synchronized void deregister(String id) {
    Subscriber s = subscribers.get(id);
    if (s == null) {
      return;
    }
    s.freeze();
    for (ObjectName source : s.sources) {
      if (!listenedToBySomeoneElse(source, s)) {
        removeListener(source);
      }
    }
    subscribers.remove(id);
  }

  private boolean listenedToBySomeoneElse(ObjectName source, Subscriber excluded) {
    for (Subscriber other : subscribers.values()) {
      if (other != excluded && other.sources.contains(source)) {
        return true;
      }
    }
    return false;
  }

  private void removeListener(ObjectName source) {
    NotificationListener l = listeners.get(source);
    if (l == null) {
      return;
    }
    try {
      mbs.removeNotificationListener(source, l);
    } catch (javax.management.InstanceNotFoundException
        | javax.management.ListenerNotFoundException absent) {
      // The broadcaster or listener has already gone; no resource remains to release.
    } catch (Exception error) {
      throw new IllegalStateException("remove notification listener from " + source, error);
    }
    listeners.remove(source);
  }

  /** Remove all listeners — called on webapp shutdown. */
  public synchronized void shutdown() {
    RuntimeException failure = null;
    for (String id : new ArrayList<>(subscribers.keySet())) {
      try {
        deregister(id);
      } catch (RuntimeException error) {
        if (failure == null) failure = error;
        else failure.addSuppressed(error);
      }
    }
    if (failure != null) throw failure;
  }

  /** Subscribers currently registered (for diagnostics). */
  public List<Map<String, Object>> list() {
    List<Map<String, Object>> out = new ArrayList<>();
    for (Subscriber s : subscribers.values()) {
      Map<String, Object> m = new LinkedHashMap<>();
      m.put("id", s.id);
      m.put("mbean", s.target.getCanonicalName());
      m.put("filters", s.filters);
      m.put("capacity", s.capacity);
      m.put("queued", s.queue.size());
      m.put("dropped", s.dropped.get());
      m.put("active", s.isActive());
      out.add(m);
    }
    return out;
  }
}
