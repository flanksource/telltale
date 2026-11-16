package com.flanksource.telltale.diag.hotpatch;

import java.lang.instrument.*;
import java.security.ProtectionDomain;
import java.util.*;
import org.objectweb.asm.*;
import org.objectweb.asm.tree.*;

/** Applies authored method edits to the latest foreign transformer output. */
public final class SourceTransformations implements ClassFileTransformer {
  private static final Map<Class<?>, Patch> PATCHES = Collections.synchronizedMap(new WeakHashMap<>());
  private static Instrumentation instrumentation;

  public static synchronized void install(Instrumentation installed) {
    if (!TransformOwner.owns(SourceTransformations.class)) {
      TransformOwner.invoke(SourceTransformations.class, "install", new Object[] {installed}); return;
    }
    if (instrumentation != null) return;
    if (!installed.isRetransformClassesSupported()) throw new IllegalStateException("source patches require class retransformation");
    installed.addTransformer(new SourceTransformations(), true);
    instrumentation = installed;
  }

  static synchronized void apply(Instrumentation installed, List<ClassDefinition> definitions) {
    if (!TransformOwner.owns(SourceTransformations.class)) {
      TransformOwner.invoke(SourceTransformations.class, "apply", new Object[] {installed, definitions}); return;
    }
    install(installed);
    Map<Class<?>, Patch> previous = new LinkedHashMap<>();
    Map<Class<?>, Patch> prepared = new LinkedHashMap<>();
    boolean published = false;
    try {
      for (ClassDefinition definition : definitions) {
        Class<?> type = definition.getDefinitionClass();
        com.flanksource.telltale.observer.ObserverRuntime.assertTargetActive(type);
        if (!installed.isModifiableClass(type)) throw new IllegalStateException("source target is not modifiable: " + type.getName());
        Patch before = PATCHES.get(type);
        previous.put(type, before);
        ClassNode current = read(PatchService.sourceBytes(installed, type));
        ClassNode replacement = read(definition.getDefinitionClassFile());
        requireSameSchema(current, replacement);
        Map<String, MethodNode> methods = before == null ? new LinkedHashMap<>() : new LinkedHashMap<>(before.methods);
        Map<String, MethodNode> existing = methods(current);
        Map<String, MethodNode> origins = null;
        for (MethodNode method : replacement.methods) {
          String key = key(method);
          if (Arrays.equals(fingerprint(existing.get(key)), fingerprint(method))) continue;
          MethodNode owned = before == null ? null : before.methods.get(key);
          if (owned == null || !digest(existing.get(key)).equals(digest(owned))) {
            if (origins == null) origins = SourceMethodOrigins.read(type, method);
            MethodNode origin = origins.get(key);
            if (origin == null || !digest(existing.get(key)).equals(digest(origin)))
              throw SourceMethodOrigins.unavailable(type, method, "runtime method differs from its code-source artifact; an uncoordinated transform would be overwritten");
          }
          methods.put(key, method);
        }
        prepared.put(type, new Patch(methods));
      }
      for (Map.Entry<Class<?>, Patch> entry : prepared.entrySet()) PATCHES.put(entry.getKey(), entry.getValue());
      published = true;
      retransform(previous.keySet());
      for (Class<?> type : previous.keySet())
        if (PATCHES.get(type).failure != null) throw new IllegalStateException("cannot patch source of " + type.getName(), PATCHES.get(type).failure);
    } catch (RuntimeException | LinkageError error) {
      if (!published) throw error;
      for (Map.Entry<Class<?>, Patch> entry : previous.entrySet()) {
        if (entry.getValue() == null) PATCHES.remove(entry.getKey()); else PATCHES.put(entry.getKey(), entry.getValue());
      }
      retransform(previous.keySet());
      throw error;
    }
  }

  static synchronized void restore(Collection<Class<?>> types) {
    if (!TransformOwner.owns(SourceTransformations.class)) {
      TransformOwner.invoke(SourceTransformations.class, "restore", new Object[] {types}); return;
    }
    for (Class<?> type : types)
      if (!PATCHES.containsKey(type)) throw new NoSuchElementException("no authored source edit for " + type.getName());
    for (Class<?> type : types) com.flanksource.telltale.observer.ObserverRuntime.assertTargetActive(type);
    Map<Class<?>, Patch> previous = new LinkedHashMap<>();
    for (Class<?> type : types) previous.put(type, PATCHES.remove(type));
    try { retransform(types); }
    catch (RuntimeException | LinkageError error) {
      for (Map.Entry<Class<?>, Patch> entry : previous.entrySet())
        if (entry.getValue() != null) PATCHES.put(entry.getKey(), entry.getValue());
      retransform(types);
      throw error;
    }
  }

  static boolean contains(Class<?> type) {
    if (!TransformOwner.owns(SourceTransformations.class))
      return Boolean.TRUE.equals(TransformOwner.invoke(SourceTransformations.class, "contains", new Object[] {type}));
    return PATCHES.containsKey(type);
  }

  @SuppressWarnings("unchecked")
  public static Object dispatch(String operation, Object[] args) {
    switch (operation) {
      case "install": install((Instrumentation) args[0]); return null;
      case "apply": apply((Instrumentation) args[0], (List<ClassDefinition>) args[1]); return null;
      case "restore": restore((Collection<Class<?>>) args[0]); return null;
      case "contains": return contains((Class<?>) args[0]);
      case "source": return PatchService.sourceBytes((Instrumentation) args[0], (Class<?>) args[1]);
      default: throw new IllegalArgumentException("unknown source transformation operation " + operation);
    }
  }

  public byte[] transform(ClassLoader loader, String name, Class<?> type, ProtectionDomain domain, byte[] bytes) {
    Patch patch = type == null ? null : PATCHES.get(type);
    if (patch == null) return null;
    try {
      ClassNode current = read(bytes);
      for (int i = 0; i < current.methods.size(); i++) {
        MethodNode original = current.methods.get(i);
        MethodNode replacement = patch.methods.get(key(original));
        if (replacement != null) current.methods.set(i, replacement);
      }
      ClassWriter writer = new ClassWriter(ClassWriter.COMPUTE_MAXS);
      current.accept(writer);
      return writer.toByteArray();
    } catch (Throwable error) { patch.failure = error; return null; }
  }

  private static void retransform(Collection<Class<?>> types) {
    if (types.isEmpty()) return;
    try { instrumentation.retransformClasses(types.toArray(new Class<?>[0])); }
    catch (UnmodifiableClassException error) { throw new IllegalStateException("cannot retransform source patches", error); }
  }

  private static ClassNode read(byte[] bytes) {
    ClassNode node = new ClassNode();
    new ClassReader(bytes).accept(node, 0);
    return node;
  }

  private static String key(MethodNode method) { return method.name + method.desc; }
  private static String digest(MethodNode method) { return BytecodeSource.digest(fingerprint(method)); }
  private static Map<String, MethodNode> methods(ClassNode node) {
    Map<String, MethodNode> methods = new LinkedHashMap<>();
    for (MethodNode method : node.methods) methods.put(key(method), method);
    return methods;
  }

  private static byte[] fingerprint(MethodNode method) {
    ClassWriter writer = new ClassWriter(0);
    writer.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, "MethodBody", null, "java/lang/Object", null);
    method.accept(writer);
    writer.visitEnd();
    ClassWriter normalized = new ClassWriter(0);
    new ClassReader(writer.toByteArray()).accept(normalized, ClassReader.SKIP_DEBUG | ClassReader.SKIP_FRAMES);
    return normalized.toByteArray();
  }

  private static void requireSameSchema(ClassNode current, ClassNode replacement) {
    if (!Objects.equals(current.name, replacement.name) || current.access != replacement.access
        || !Objects.equals(current.superName, replacement.superName) || !current.interfaces.equals(replacement.interfaces)
        || !schema(current).equals(schema(replacement)))
      throw new IllegalArgumentException("source patches may change method bodies only: " + current.name);
  }

  private static Set<String> schema(ClassNode node) {
    Set<String> schema = new TreeSet<>();
    for (FieldNode field : node.fields) schema.add("field:" + field.name + field.desc + ":" + field.access);
    for (MethodNode method : node.methods) schema.add("method:" + key(method) + ":" + method.access);
    return schema;
  }

  private static final class Patch {
    final Map<String, MethodNode> methods;
    volatile Throwable failure;
    Patch(Map<String, MethodNode> methods) {
      this.methods = Collections.unmodifiableMap(methods);
    }
  }
}
