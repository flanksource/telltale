package com.flanksource.telltale.diag.graph;

import java.lang.reflect.Field;

/**
 * What a probe snapshot may render as text, or walk element by element, on application's own
 * thread.
 *
 * <p>A class under {@code java.} is not enough on its own. Some JDK classes render by running what
 * the application handed them — {@code CharBuffer.wrap(sequence)}, a {@code LongAccumulator}'s
 * function — and some take their own monitor in every method: another thread can hold a {@code
 * Vector}, {@code Hashtable} or {@code StringBuffer} while it runs foreign code ({@code indexOf}
 * calls {@code equals}, {@code append} calls {@code toString}) that waits on a lock application's
 * thread holds. So both lists name exact classes, and everything else is walked by its fields,
 * which reads without calling or locking anything.
 */
public final class InertTypes {

  private InertTypes() {}

  /**
   * Whether a probe renders {@code value} as one line of text. An enum renders by {@code name()},
   * which is final; a class by its name; an exception by {@link #throwableText}.
   */
  static boolean scalar(Class<?> type, Object value) {
    if (value instanceof Enum || value instanceof Class || value instanceof Throwable) {
      return true;
    }
    switch (type.getName()) {
      case "java.lang.String":
      case "java.lang.StringBuilder":
      case "java.lang.Boolean":
      case "java.lang.Character":
      case "java.lang.Byte":
      case "java.lang.Short":
      case "java.lang.Integer":
      case "java.lang.Long":
      case "java.lang.Float":
      case "java.lang.Double":
      case "java.math.BigDecimal":
      case "java.math.BigInteger":
      case "java.util.concurrent.atomic.AtomicBoolean":
      case "java.util.concurrent.atomic.AtomicInteger":
      case "java.util.concurrent.atomic.AtomicLong":
      case "java.util.concurrent.atomic.LongAdder":
      case "java.util.concurrent.atomic.DoubleAdder":
      case "java.util.Date":
      case "java.sql.Date":
      case "java.sql.Time":
      case "java.sql.Timestamp":
      case "java.util.UUID":
      case "java.util.Currency":
      case "java.util.Locale":
      case "java.time.Instant":
      case "java.time.LocalDate":
      case "java.time.LocalDateTime":
      case "java.time.LocalTime":
      case "java.time.OffsetDateTime":
      case "java.time.OffsetTime":
      case "java.time.ZonedDateTime":
      case "java.time.Duration":
      case "java.time.Period":
      case "java.time.Year":
      case "java.time.YearMonth":
      case "java.time.MonthDay":
      case "java.time.ZoneOffset":
      case "java.time.ZoneRegion":
        return true;
      default:
        return false;
    }
  }

  /**
   * Whether a probe walks a map or iterable of this class element by element: only concrete JDK
   * collections whose iteration touches nothing but their own storage, and takes no monitor.
   *
   * <p>A wrapper such as {@code Collections$UnmodifiableList} is left out on purpose: it delegates
   * to whatever it wraps, which may be a persistent collection that loads on first touch. Walked by
   * its fields instead, it still shows what it wraps. {@code WeakHashMap} is left out because
   * iterating it expunges entries.
   */
  static boolean iterates(Class<?> type) {
    switch (type.getName()) {
      case "java.util.ArrayList":
      case "java.util.LinkedList":
      case "java.util.Arrays$ArrayList":
      case "java.util.ArrayDeque":
      case "java.util.HashMap":
      case "java.util.LinkedHashMap":
      case "java.util.TreeMap":
      case "java.util.IdentityHashMap":
      case "java.util.EnumMap":
      case "java.util.HashSet":
      case "java.util.LinkedHashSet":
      case "java.util.TreeSet":
      case "java.util.Collections$EmptyList":
      case "java.util.Collections$EmptyMap":
      case "java.util.Collections$EmptySet":
      case "java.util.Collections$SingletonList":
      case "java.util.Collections$SingletonMap":
      case "java.util.Collections$SingletonSet":
      case "java.util.concurrent.ConcurrentHashMap":
      case "java.util.concurrent.CopyOnWriteArrayList":
      case "java.util.concurrent.ConcurrentLinkedQueue":
        return true;
      default:
        return false;
    }
  }

  /**
   * An exception as its class and message. A JDK exception's own {@code getMessage()} is the JDK's;
   * any other class may override it, so its message is read from {@code Throwable}'s field instead.
   * That read needs {@code setAccessible}, which Java 8 — what application runs — allows and a JDK
   * 9+ module system refuses; refused, the class name alone still says what was thrown.
   */
  public static String throwableText(Throwable error) {
    String name = error.getClass().getName();
    String message = null;
    if (name.startsWith("java.")) {
      message = error.getMessage();
    } else {
      try {
        Field detail = Throwable.class.getDeclaredField("detailMessage");
        detail.setAccessible(true);
        message = (String) detail.get(error);
      } catch (ReflectiveOperationException | RuntimeException e) {
        message = null;
      }
    }
    return message == null ? name : name + ": " + message;
  }
}
