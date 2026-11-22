package com.flanksource.telltale.diag.hotpatch;

import java.lang.instrument.ClassDefinition;
import java.lang.instrument.Instrumentation;
import java.lang.reflect.Field;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.NoSuchElementException;
import com.flanksource.telltale.observer.ObserverRuntime;

/**
 * Applies and reverses authored method edits through the JVM transformation chain.
 *
 * <p>The source edit coordinator and original-byte registry live on the system class path, so a
 * diagnostics WAR redeploy can still inspect and restore edits installed by its previous instance.
 *
 * <p>What the JVM permits is narrow, and deliberately not papered over: HotSpot redefinition may
 * change method bodies only. Adding or removing a method or field, changing a signature, or
 * changing the class hierarchy is rejected by the JVM, and this reports that as the schema-change
 * limitation it is rather than as a mystery.
 */
public final class PatchService {
  enum RestoreScope { ALL, SOURCE, PROBES }
  static final class RestoreRequest {
    final ClassIndex.Entry entry;
    final RestoreScope scope;
    final String leaseId;
    RestoreRequest(ClassIndex.Entry entry, RestoreScope scope, String leaseId) {
      this.entry = entry;
      this.scope = scope;
      this.leaseId = leaseId;
    }
  }

  private static final String ORIGINALS_FIELD = "ORIGINAL_BYTES";

  private final Instrumentation instrumentation;
  private final BytecodeSource bytecode;

  public PatchService(Instrumentation instrumentation) {
    this.instrumentation = instrumentation;
    this.bytecode = new BytecodeSource(instrumentation);
  }

  /** {@code <loaderHash>|<binary name>} — a class is only unique per loader. */
  static String key(String loaderHash, String className) {
    return loaderHash + "|" + className;
  }

  /**
   * The pre-patch bytes for one class: the stashed ones when this is a re-patch, read from the JVM
   * only the first time.
   *
   * <p>Not just an optimisation. Reading is a retransformation, and on a JVM whose transformer
   * chain rebuilds the class that reverts the patch already installed — so re-reading on every
   * patch would undo the previous span on the way to arming the next one, and would then record the
   * bytes it just reverted TO as this patch's "original".
   */
  private byte[] preservedOriginal(ClassIndex.Entry entry, String className, Class<?> type) {
    byte[] stashed = originals().get(key(entry.getLoaderHash(), className));
    return stashed != null ? stashed : sourceBytes(instrumentation, type);
  }

  static byte[] sourceBytes(Instrumentation instrumentation, Class<?> type) {
    if (!TransformOwner.owns(SourceTransformations.class)) return (byte[]) TransformOwner.invoke(
        SourceTransformations.class, "source", new Object[] {instrumentation, type});
    if (!ObserverRuntime.installed()) return ProbeTransformations.contains(type)
        ? ProbeTransformations.sourceBytes(type) : new BytecodeSource(instrumentation).bytecodeOf(type);
    try { return ObserverRuntime.sourceBytes(type); }
    catch (Exception error) { throw new IllegalStateException("read source bytecode for " + type.getName(), error); }
  }

  /**
   * Installs the changed methods from {@code compiled}, remembering pre-patch bytes for reporting.
   *
   * @param compiled every class javac produced — the top-level class plus its nested and anonymous
   *     classes, which must be redefined in the same call to stay consistent with each other.
   */
  public Map<String, Object> apply(
      ClassIndex index, ClassIndex.Entry entry, Map<String, byte[]> compiled, String leaseId) {
    SourceTransformations.install(instrumentation);
    // Resolve the stash before anything is redefined. It is the only route
    // back, and a JVM still running an older agent jar does not have one —
    // reaching it afterwards would leave the class patched with no undo.
    Map<String, byte[]> registry = originals();
    Class<?> target = liveClass(entry);
    byte[] replacement = compiled.get(entry.getName());
    if (replacement == null) {
      throw new IllegalArgumentException(
          "compiled output has no class named "
              + entry.getName()
              + " (has "
              + compiled.keySet()
              + ")");
    }

    List<ClassDefinition> definitions = new ArrayList<>();
    Map<String, byte[]> originals = new LinkedHashMap<>();
    originals.put(entry.getName(), preservedOriginal(entry, entry.getName(), target));
    definitions.add(new ClassDefinition(target, replacement));

    // Nested classes only come along when they are already loaded; a newly
    // introduced one cannot be defined by a redefinition and is reported.
    for (Map.Entry<String, byte[]> produced : compiled.entrySet()) {
      if (produced.getKey().equals(entry.getName())) {
        continue;
      }
      ClassIndex.Entry nested = findLoaded(index, entry, produced.getKey());
      if (nested == null) {
        throw new IllegalStateException(
            "compiling "
                + entry.getName()
                + " produced a new class "
                + produced.getKey()
                + " that is not loaded in the JVM."
                + " A redefinition cannot introduce classes — remove the new nested or anonymous"
                + " class (a lambda or anonymous inner class in added code is the usual cause)");
      }
      Class<?> nestedType = liveClass(nested);
      originals.put(produced.getKey(), preservedOriginal(entry, produced.getKey(), nestedType));
      definitions.add(new ClassDefinition(nestedType, produced.getValue()));
    }

    SourceTransformations.apply(instrumentation, definitions, leaseId);

    for (Map.Entry<String, byte[]> original : originals.entrySet()) {
      // putIfAbsent: the first patch holds the true original. Re-patching a
      // patched class must not overwrite it with already-modified bytes.
      registry.putIfAbsent(key(entry.getLoaderHash(), original.getKey()), original.getValue());
    }
    return appliedStatus(entry, originals.get(entry.getName()), replacement);
  }

  /**
   * What a patch installed, reported WITHOUT reading the class back.
   *
   * <p>Reading back is not free here: {@link BytecodeSource#bytecodeOf} is implemented as
   * retransformClasses, which re-runs every retransform-capable transformer in the JVM and installs
   * the result. On a JVM where another transformer contributes bytes derived from its own cached
   * original, that re-read UNDOES the redefinition it was meant to verify — measured on the dev
   * cycle JVM, where the bytes handed over digested fdba8856 and the class read back as its
   * untouched ce23c9b1, with zero probe references left in it and no error anywhere. The same call
   * against the web JVM patched cleanly, which is why this looked JVM-specific rather than
   * self-inflicted.
   *
   * <p>So the report is built from the bytes handed to the JVM. That is a real trade: {@link
   * #status} derives from live bytes precisely so it cannot drift from what is running, and this
   * cannot make that claim. It states what was installed, and leaves the class alone — which is the
   * only way the patch survives being described.
   */
  private Map<String, Object> appliedStatus(
      ClassIndex.Entry entry, byte[] original, byte[] handed) {
    String originalDigest = BytecodeSource.digest(original);
    String handedDigest = BytecodeSource.digest(handed);
    Map<String, Object> out = new LinkedHashMap<>();
    out.put("class", entry.getName());
    out.put("loader", entry.getLoaderHash());
    out.put("digest", handedDigest);
    out.put("originalDigest", originalDigest);
    out.put("handedDigest", handedDigest);
    out.put("patched", !originalDigest.equals(handedDigest));
    out.put("leaseExpiresAt", AgentBridge.leaseExpiresAt(entry.getLoaderHash(), entry.getName()));
    // A rewrite that changed nothing is the other silent failure: the JVM
    // takes the definition, the class is identical, and every layer above
    // reports success over a method that was never instrumented.
    if (originalDigest.equals(handedDigest)) {
      out.put(
          "error",
          "the rewrite of "
              + entry.getName()
              + " (loader "
              + entry.getLoaderHash()
              + ") produced bytes identical to the original, so nothing was instrumented"
              + " and nothing will be recorded");
    }
    return out;
  }

  /** Removes authored edits and rebuilds through the current foreign transformer chain. */
  public Map<String, Object> restore(ClassIndex index, RestoreRequest request) {
    ClassIndex.Entry entry = request.entry;
    Map<String, byte[]> registry = originals();
    if (request.scope != RestoreScope.SOURCE && ProbeTransformations.contains(liveClass(entry))) {
      ProbeTransformations.restore(liveClass(entry));
      if (request.scope == RestoreScope.PROBES || !registry.containsKey(key(entry.getLoaderHash(), entry.getName()))) {
        Map<String, Object> result = new LinkedHashMap<>(heldStatus(entry));
        result.put("class", entry.getName()); result.put("loader", entry.getLoaderHash());
        result.put("restored", java.util.Collections.singletonList(entry.getName()));
        return result;
      }
    }
    if (request.scope == RestoreScope.PROBES) throw new NoSuchElementException("no method probes are armed on " + entry.getName());
    String prefix = key(entry.getLoaderHash(), entry.getName());
    List<Class<?>> restoredTypes = new ArrayList<>();
    List<String> restored = new ArrayList<>();
    for (Map.Entry<String, byte[]> stored : registry.entrySet()) {
      String storedKey = stored.getKey();
      if (!storedKey.equals(prefix) && !storedKey.startsWith(prefix + "$")) {
        continue;
      }
      String className = storedKey.substring(storedKey.indexOf('|') + 1);
      ClassIndex.Entry loaded = findLoaded(index, entry, className);
      if (loaded == null) {
        continue;
      }
      restoredTypes.add(liveClass(loaded));
      restored.add(className);
    }
    if (restoredTypes.isEmpty()) {
      throw new NoSuchElementException(
          entry.getName()
              + " is not patched"
              + " (loader "
              + entry.getLoaderHash()
              + "), so there is nothing to restore");
    }
    String handedDigest = BytecodeSource.digest(registry.get(prefix));
    SourceTransformations.restore(restoredTypes, request.leaseId);
    for (String className : restored) {
      registry.remove(key(entry.getLoaderHash(), className));
    }
    Map<String, Object> out = new LinkedHashMap<>();
    out.put("class", entry.getName());
    out.put("loader", entry.getLoaderHash());
    out.put("patched", ProbeTransformations.contains(liveClass(entry)));
    out.put("restored", restored);
    out.put("digest", handedDigest);
    out.put("handedDigest", handedDigest);
    return out;
  }

  /**
   * Every class whose bytes currently differ from the originals held for it.
   *
   * <p>Derived by comparing live bytes against the stash rather than tracked as metadata, so it
   * stays true across a WAR redeploy and cannot drift from what the JVM is actually running.
   */
  public List<Map<String, Object>> patches(ClassIndex index) {
    List<Map<String, Object>> out = new ArrayList<>();
    for (Class<?> type : ProbeTransformations.types()) {
      ClassIndex.Entry entry = ClassIndex.Entry.of(type);
      if (!originals().containsKey(key(entry.getLoaderHash(), entry.getName())))
        out.add(status(index, entry.getLoaderHash(), entry.getName()));
    }
    for (Map.Entry<String, byte[]> stored : originals().entrySet()) {
      int split = stored.getKey().indexOf('|');
      if (split < 0) {
        continue;
      }
      String loaderHash = stored.getKey().substring(0, split);
      String className = stored.getKey().substring(split + 1);
      try {
        out.add(status(index, loaderHash, className));
      } catch (NoSuchElementException | IllegalStateException e) {
        // The class went away with its classloader (a webapp redeploy);
        // the stash entry is stale but harmless, and saying so beats
        // failing the whole listing.
        Map<String, Object> row = new LinkedHashMap<>();
        row.put("class", className);
        row.put("loader", loaderHash);
        row.put("patched", false);
        row.put("error", "no longer loaded: " + e.getMessage());
        out.add(row);
      }
    }
    out.sort(Comparator.comparing(row -> String.valueOf(row.get("class"))));
    return out;
  }

  /**
   * Live digest vs the stashed original for one class, and when the agent will put it back on its
   * own.
   *
   * <p>{@code leaseExpiresAt} is what separates a patch a live span is holding open from one that
   * nothing will ever undo. Without it a reader can see that a class is patched but not whether
   * walking away is safe, and those are the two states a patch register exists to tell apart.
   */
  public Map<String, Object> status(ClassIndex index, String loaderHash, String className) {
    ClassIndex.Entry entry = index.resolve(className, loaderHash);
    byte[] current = bytecode.bytecodeOf(liveClass(entry));
    byte[] original = originals().get(key(entry.getLoaderHash(), className));
    String currentDigest = BytecodeSource.digest(current);
    Map<String, Object> out = new LinkedHashMap<>();
    out.put("class", className);
    out.put("loader", entry.getLoaderHash());
    out.put("digest", currentDigest);
    out.put("originalDigest", original == null ? "" : BytecodeSource.digest(original));
    out.put("patched", ProbeTransformations.contains(liveClass(entry))
        || original != null && !BytecodeSource.digest(original).equals(currentDigest));
    out.put("leaseExpiresAt", AgentBridge.leaseExpiresAt(entry.getLoaderHash(), className));
    return out;
  }

  /**
   * Whether a span still holds its patch on one class, and until when — answered from the stash and
   * the lease table, WITHOUT reading the class back.
   *
   * <p>This is the lease renewal's answer, and the renewal fires every 40s for as long as a span
   * holds the patch. {@link #status} is the wrong answer there: its read-back is a
   * retransformation, and on the cycle JVM that silently reverts the very patch being renewed (see
   * {@link #appliedStatus}). A class is held while its original is stashed; {@link #restore} is
   * what clears it.
   */
  public Map<String, Object> heldStatus(ClassIndex.Entry entry) {
    Map<String, Object> result = heldStatus(
        entry.getLoaderHash(),
        entry.getName(),
        originals(),
        AgentBridge.leaseExpiresAt(entry.getLoaderHash(), entry.getName()));
    if (ProbeTransformations.contains(liveClass(entry))) result.put("patched", true);
    return result;
  }

  static Map<String, Object> heldStatus(
      String loaderHash, String className, Map<String, byte[]> stash, long leaseExpiresAt) {
    Map<String, Object> out = new LinkedHashMap<>();
    out.put("class", className);
    out.put("loader", loaderHash);
    out.put("patched", stash.containsKey(key(loaderHash, className)));
    out.put("leaseExpiresAt", leaseExpiresAt);
    return out;
  }

  private static Class<?> liveClass(ClassIndex.Entry entry) {
    Class<?> type = entry.getType();
    if (type == null) {
      throw new IllegalStateException("no live Class handle for " + entry.getName());
    }
    return type;
  }

  /** The same class name under the same loader as the class being patched. */
  private static ClassIndex.Entry findLoaded(
      ClassIndex index, ClassIndex.Entry sibling, String className) {
    try {
      return index.resolve(className, sibling.getLoaderHash());
    } catch (NoSuchElementException e) {
      return null;
    }
  }

  /**
   * The stash on the system class path, reached reflectively because the agent jar and this WAR are
   * loaded by different classloaders. Both the map and its key/value types are JDK types, so it
   * crosses that boundary safely.
   */
  @SuppressWarnings("unchecked")
  static Map<String, byte[]> originals() {
    try {
      Class<?> holder =
          ClassLoader.getSystemClassLoader().loadClass(InstrumentationService.AGENT_CLASS);
      Field field = holder.getField(ORIGINALS_FIELD);
      Object value = field.get(null);
      if (value == null) {
        throw new IllegalStateException(
            InstrumentationService.AGENT_CLASS
                + "."
                + ORIGINALS_FIELD
                + " is null — the attached agent jar predates hot patching; redeploy"
                + " telltale-diag");
      }
      return (Map<String, byte[]>) value;
    } catch (ClassNotFoundException | NoSuchFieldException e) {
      throw new IllegalStateException(
          "the attached agent jar has no "
              + ORIGINALS_FIELD
              + " stash, so patches could not be reversed after a redeploy."
              + " Restart the JVM to pick up the current agent: "
              + e,
          e);
    } catch (IllegalAccessException e) {
      throw new IllegalStateException(
          "cannot read " + InstrumentationService.AGENT_CLASS + "." + ORIGINALS_FIELD + ": " + e,
          e);
    }
  }
}
