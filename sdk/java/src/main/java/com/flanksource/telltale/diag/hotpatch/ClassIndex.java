package com.flanksource.telltale.diag.hotpatch;

import java.lang.instrument.Instrumentation;
import java.lang.reflect.Method;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.Comparator;
import java.util.IdentityHashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.NoSuchElementException;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Read-only view over the classes currently loaded in this JVM, grouped by classloader. Backs
 * contract §1 ({@code /classes/loaders}) and §2 ({@code /classes}).
 *
 * <p>Loader ids are {@code Integer.toHexString(System.identityHashCode(cl))} — the same convention
 * Arthas uses, so both backends address the same loader with the same id. The bootstrap loader
 * ({@code null}) is {@code "0"}.
 *
 * <p>A loader id may also be a <em>virtual</em> id (see {@link LoaderProfile}), which stands for a
 * whole family of loaders at once. Virtual ids are a listing filter: the classes they return still
 * report the real loader that defined them, so decompiling or patching from a merged listing
 * addresses a concrete loader. {@link #resolveForWrite} rejects them outright.
 */
public final class ClassIndex {

  public static final String BOOTSTRAP_HASH = "0";
  public static final String BOOTSTRAP_LABEL = "JDK bootstrap";
  public static final int DEFAULT_LIMIT = 200;
  public static final int MAX_LIMIT = 1000;

  /** Tomcat's WebappClassLoaderBase.toString() prints the context on its own line. */
  private static final Pattern TOSTRING_CONTEXT =
      Pattern.compile("(?m)^\\s*context:\\s*(\\S+)\\s*$");

  /**
   * Package prefixes owned by the Java platform and the EE APIs it ships: everything nobody
   * browsing an application JVM is looking for. They are the bulk of {@code getAllLoadedClasses()}
   * and drown the application classes, so the listing hides them unless a caller asks for them
   * back.
   *
   * <p>Prefix-matched on the fully-qualified name, so {@code javax.} covers {@code javax.servlet}
   * and {@code javax.persistence} alike.
   */
  private static final List<String> PLATFORM_PREFIXES =
      Collections.unmodifiableList(
          Arrays.asList(
              "java.",
              "javax.",
              "jakarta.",
              "jdk.",
              "sun.",
              "com.sun.",
              "org.w3c.dom.",
              "org.xml.sax.",
              "org.omg.",
              "org.ietf.jgss.",
              "netscape.javascript."));

  /**
   * @return true when the name belongs to the JDK/EE platform rather than to an application.
   */
  public static boolean isPlatform(String className) {
    for (String prefix : PLATFORM_PREFIXES) {
      if (className.startsWith(prefix)) {
        return true;
      }
    }
    return false;
  }

  /** One loaded class: its binary name, its defining loader, and (in production) the live Class. */
  public static final class Entry {
    private final String name;
    private final ClassLoader loader;
    private final Class<?> type;

    public Entry(String name, ClassLoader loader) {
      this(name, loader, null);
    }

    private Entry(String name, ClassLoader loader, Class<?> type) {
      if (name == null || name.isEmpty()) {
        throw new IllegalArgumentException("class name is required");
      }
      this.name = name;
      this.loader = loader;
      this.type = type;
    }

    public static Entry of(Class<?> type) {
      return new Entry(type.getName(), type.getClassLoader(), type);
    }

    public String getName() {
      return name;
    }

    public ClassLoader getLoader() {
      return loader;
    }

    public String getLoaderHash() {
      return hash(loader);
    }

    /** The live class, or {@code null} for a synthetic entry. */
    public Class<?> getType() {
      return type;
    }
  }

  private final List<Entry> entries;
  private Map<String, String> groupsByHash;

  public ClassIndex(List<Entry> entries) {
    this.entries = entries;
  }

  public static ClassIndex from(Instrumentation instrumentation) {
    Class<?>[] loaded = instrumentation.getAllLoadedClasses();
    List<Entry> entries = new ArrayList<>(loaded.length);
    for (Class<?> type : loaded) {
      if (isBrowsable(type)) {
        entries.add(Entry.of(type));
      }
    }
    return new ClassIndex(entries);
  }

  /**
   * Arrays and primitives have no class file, so they can be neither browsed nor decompiled. They
   * also report JVM descriptor names — {@code String[].class.getName()} is {@code
   * "[Ljava.lang.String;"} — which the package tree splits on {@code '.'} into junk nodes like
   * {@code "[[Lcom"}. They have no place in a browse index.
   *
   * <p>Package-visible so this is testable without a live {@link Instrumentation}.
   */
  static boolean isBrowsable(Class<?> type) {
    return !type.isArray() && !type.isPrimitive();
  }

  public static String hash(ClassLoader loader) {
    return loader == null ? BOOTSTRAP_HASH : Integer.toHexString(System.identityHashCode(loader));
  }

  /** What this loader is, in words. See {@link LoaderProfile} for the rules. */
  public static String label(ClassLoader loader) {
    return LoaderProfile.of(loader).getLabel();
  }

  /** Webapp context path when derivable, else {@code null}. */
  static String contextOf(ClassLoader loader) {
    for (String accessor : new String[] {"getContextName", "getContextPath"}) {
      String value = invokeAccessor(loader, accessor);
      String normalized = normalizeContext(value);
      if (normalized != null) {
        return normalized;
      }
    }
    Matcher matcher = TOSTRING_CONTEXT.matcher(String.valueOf(loader));
    return matcher.find() ? normalizeContext(matcher.group(1)) : null;
  }

  private static String invokeAccessor(ClassLoader loader, String name) {
    for (Class<?> type = loader.getClass();
        type != null && type != Object.class;
        type = type.getSuperclass()) {
      try {
        Method method = type.getDeclaredMethod(name);
        if (method.getReturnType() != String.class) {
          continue;
        }
        method.setAccessible(true);
        return (String) method.invoke(loader);
      } catch (ReflectiveOperationException | RuntimeException ignored) {
        // Not a Tomcat webapp loader, or the accessor is guarded — fall through.
      }
    }
    return null;
  }

  private static String normalizeContext(String value) {
    if (value == null) {
      return null;
    }
    String trimmed = value.trim();
    if (trimmed.isEmpty() || "/".equals(trimmed) || "null".equals(trimmed)) {
      return null;
    }
    return trimmed.startsWith("/") ? trimmed : "/" + trimmed;
  }

  /**
   * Contract §1: one row per loader, application code first.
   *
   * <p>Loader families that exist in the thousands — one loader per generated reflection accessor,
   * one per runtime-compiled application rule — collapse into a single virtual row each, because a
   * picker with 2,171 entries is not a picker. Members stay individually addressable by their real
   * hash: the virtual id is a filter, not a rename.
   */
  public List<Map<String, Object>> loaders() {
    Map<ClassLoader, List<String>> byLoader = new IdentityHashMap<>();
    List<String> bootstrap = new ArrayList<>();
    for (Entry entry : entries) {
      if (entry.loader == null) {
        bootstrap.add(entry.name);
        continue;
      }
      List<String> names = byLoader.get(entry.loader);
      if (names == null) {
        names = new ArrayList<>();
        byLoader.put(entry.loader, names);
      }
      names.add(entry.name);
    }

    return LoaderRows.build(byLoader, bootstrap);
  }

  public boolean hasLoader(String loaderHash) {
    for (Entry entry : entries) {
      if (matchesLoader(entry, loaderHash)) {
        return true;
      }
    }
    return false;
  }

  /** True when {@code loaderHash} names this entry's loader, or a family it belongs to. */
  private boolean matchesLoader(Entry entry, String loaderHash) {
    if (entry.getLoaderHash().equals(loaderHash)) {
      return true;
    }
    return LoaderProfile.isGroupId(loaderHash)
        && loaderHash.equals(groupsByHash().get(entry.getLoaderHash()));
  }

  /**
   * Real loader hash to the virtual id it merges into, for the grouped loaders only. Built once per
   * index because {@link LoaderProfile#of} can reach for the reflective webapp-context probe, which
   * must not run per entry.
   */
  private Map<String, String> groupsByHash() {
    if (groupsByHash == null) {
      Map<ClassLoader, Boolean> seen = new IdentityHashMap<>();
      Map<String, String> found = new LinkedHashMap<>();
      for (Entry entry : entries) {
        if (entry.loader == null || seen.put(entry.loader, Boolean.TRUE) != null) {
          continue;
        }
        String group = LoaderProfile.of(entry.loader).getGroup();
        if (group != null) {
          found.put(entry.getLoaderHash(), group);
        }
      }
      groupsByHash = found;
    }
    return groupsByHash;
  }

  /**
   * Contract §2: {@code total} counts every match; {@code classes} is the sorted page. {@code
   * platformExcluded} reports how many otherwise-matching classes the platform filter hid, so the
   * omission is visible rather than silent; it is 0 when {@code includePlatform} is set.
   */
  public Map<String, Object> page(
      String loaderHash, String query, int limit, int offset, boolean includePlatform) {
    Matches matches = matches(loaderHash, query, includePlatform);
    matches.entries.sort(order(query));
    List<Map<String, Object>> classes = new ArrayList<>();
    for (int i = offset; i < matches.entries.size() && classes.size() < limit; i++) {
      Map<String, Object> row = new LinkedHashMap<>();
      row.put("name", matches.entries.get(i).getName());
      row.put("loader", matches.entries.get(i).getLoaderHash());
      classes.add(row);
    }
    Map<String, Object> out = new LinkedHashMap<>();
    out.put("total", matches.entries.size());
    out.put("offset", offset);
    out.put("limit", limit);
    out.put("platformExcluded", matches.platformExcluded);
    out.put("classes", classes);
    return out;
  }

  /**
   * Name order without a query. With one, a picker reads only the head of the page, so matches rank
   * by how the simple name matches — exactly, as a prefix, anywhere — before a match that is only
   * in the package path; within a rank named classes come before inner and anonymous ones.
   */
  private static Comparator<Entry> order(String query) {
    Comparator<Entry> byName =
        Comparator.comparing(Entry::getName).thenComparing(Entry::getLoaderHash);
    String needle = query == null ? "" : query.trim().toLowerCase(Locale.ROOT);
    if (needle.isEmpty()) {
      return byName;
    }
    return Comparator.<Entry>comparingInt(entry -> matchRank(entry.getName(), needle))
        .thenComparing(entry -> entry.getName().indexOf('$') >= 0)
        .thenComparing(byName);
  }

  private static int matchRank(String className, String needle) {
    String simpleName =
        className.substring(className.lastIndexOf('.') + 1).toLowerCase(Locale.ROOT);
    if (simpleName.equals(needle)) {
      return 0;
    }
    if (simpleName.startsWith(needle)) {
      return 1;
    }
    return simpleName.contains(needle) ? 2 : 3;
  }

  /** Matching entries plus the count the platform filter removed from them. */
  private static final class Matches {
    private final List<Entry> entries = new ArrayList<>();
    private int platformExcluded;
  }

  private Matches matches(String loaderHash, String query, boolean includePlatform) {
    String needle = query == null ? "" : query.trim().toLowerCase(Locale.ROOT);
    Matches matches = new Matches();
    for (Entry entry : entries) {
      if (loaderHash != null && !matchesLoader(entry, loaderHash)) {
        continue;
      }
      if (!needle.isEmpty() && !entry.getName().toLowerCase(Locale.ROOT).contains(needle)) {
        continue;
      }
      if (!includePlatform && isPlatform(entry.getName())) {
        matches.platformExcluded++;
        continue;
      }
      matches.entries.add(entry);
    }
    return matches;
  }

  /**
   * Resolves one loaded class. When {@code loaderHash} is absent and the name is defined by several
   * loaders, the loader that has loaded the most classes wins — that is the application loader
   * rather than a stray child loader.
   */
  public Entry resolve(String className, String loaderHash) {
    List<Entry> candidates = candidates(className, loaderHash);
    if (candidates.isEmpty()) {
      throw new NoSuchElementException(
          loaderHash == null
              ? "class not loaded: " + className
              : "class not loaded by loader " + loaderHash + ": " + className);
    }
    if (candidates.size() == 1) {
      return candidates.get(0);
    }
    // "Biggest loader wins" is a reasonable guess for a real hash or for no
    // hash at all. It is not reasonable for a virtual id, which stands for
    // hundreds of loaders on purpose — picking one of them silently would
    // decompile or patch a copy the caller never named.
    if (LoaderProfile.isGroupId(loaderHash)) {
      throw new IllegalArgumentException(
          className
              + " is defined by "
              + candidates.size()
              + " loaders in "
              + loaderHash
              + "; name one of "
              + loaderHashes(candidates));
    }
    Map<String, Integer> sizes = loaderSizes();
    candidates.sort(
        Comparator.comparingInt((Entry e) -> sizes.getOrDefault(e.getLoaderHash(), 0))
            .reversed()
            .thenComparing(Entry::getLoaderHash));
    return candidates.get(0);
  }

  /**
   * Resolves a class that is about to be modified. A virtual id is a listing filter and never a
   * patch target: {@code ~compiled} covers hundreds of distinct loaders, so redefining a class "in"
   * it would mutate whichever copy happened to sort first and report success either way.
   */
  public Entry resolveForWrite(String className, String loaderHash) {
    if (LoaderProfile.isGroupId(loaderHash)) {
      throw new IllegalArgumentException(
          loaderHash
              + " is a merged view of several loaders and cannot be modified; name one of "
              + loaderHashes(candidates(className, loaderHash)));
    }
    return resolve(className, loaderHash);
  }

  /** Every loaded class with this name, narrowed to {@code loaderHash} when given. */
  List<Entry> candidates(String className, String loaderHash) {
    List<Entry> candidates = new ArrayList<>();
    for (Entry entry : entries) {
      if (!entry.getName().equals(className)) {
        continue;
      }
      if (loaderHash != null && !matchesLoader(entry, loaderHash)) {
        continue;
      }
      candidates.add(entry);
    }
    return candidates;
  }

  private static List<String> loaderHashes(List<Entry> candidates) {
    List<String> hashes = new ArrayList<>(candidates.size());
    for (Entry candidate : candidates) {
      hashes.add(candidate.getLoaderHash());
    }
    Collections.sort(hashes);
    return hashes;
  }

  private Map<String, Integer> loaderSizes() {
    Map<String, Integer> sizes = new LinkedHashMap<>();
    for (Entry entry : entries) {
      String key = entry.getLoaderHash();
      sizes.put(key, sizes.getOrDefault(key, 0) + 1);
    }
    return sizes;
  }

  /**
   * Loaded {@code Outer$Inner} classes CFR needs alongside {@code Outer}.
   *
   * <p>Runtime-generated classes share the {@code Outer$} prefix without being nested types — a
   * lambda body, or a Spring CGLIB proxy of the target. They are not part of the source CFR is
   * reconstructing, so feeding CFR their bytes produces a decompilation of the wrong thing.
   */
  public List<Entry> innerClasses(Entry outer) {
    String prefix = outer.getName() + "$";
    List<Entry> inner = new ArrayList<>();
    for (Entry entry : entries) {
      if (entry.getLoader() == outer.getLoader()
          && entry.getName().startsWith(prefix)
          && !entry.getName().contains("$$Lambda$")
          && !entry.getName().contains("$$EnhancerBySpringCGLIB$$")) {
        inner.add(entry);
      }
    }
    inner.sort(Comparator.comparing(Entry::getName));
    return Collections.unmodifiableList(inner);
  }

  public static int parseLimit(String raw) {
    if (raw == null || raw.trim().isEmpty()) {
      return DEFAULT_LIMIT;
    }
    int limit = parseInt(raw, "limit");
    if (limit < 1 || limit > MAX_LIMIT) {
      throw new IllegalArgumentException(
          "limit must be between 1 and " + MAX_LIMIT + ", got " + raw);
    }
    return limit;
  }

  public static int parseOffset(String raw) {
    if (raw == null || raw.trim().isEmpty()) {
      return 0;
    }
    int offset = parseInt(raw, "offset");
    if (offset < 0) {
      throw new IllegalArgumentException("offset must not be negative, got " + raw);
    }
    return offset;
  }

  private static int parseInt(String raw, String field) {
    try {
      return Integer.parseInt(raw.trim());
    } catch (NumberFormatException e) {
      throw new IllegalArgumentException(field + " must be an integer, got " + raw);
    }
  }
}
