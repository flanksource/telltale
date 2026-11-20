package com.flanksource.telltale.observer;

import java.io.*;
import java.lang.instrument.ClassFileTransformer;
import java.security.ProtectionDomain;
import java.util.*;
import org.objectweb.asm.*;
import org.objectweb.asm.commons.AdviceAdapter;
import org.objectweb.asm.commons.Method;

/** Only method bodies are changed; retransformation composes with every existing agent. */
final class ObserverTransformer implements ClassFileTransformer {
  static final Type BRIDGE = Type.getObjectType("com/flanksource/telltale/observer/bootstrap/ObserverBridge");
  static final Method ENTER = new Method("enter", "(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;Ljava/lang/Object;[Ljava/lang/Object;)Ljava/lang/Object;");
  static final Method EXIT = new Method("exit", "(Ljava/lang/Object;Ljava/lang/Object;Ljava/lang/Throwable;)Ljava/lang/Object;");
  private volatile List<ObserverAdapter> adapters = Collections.emptyList();
  private volatile boolean enabled;
  private volatile Throwable failure;
  private final java.util.concurrent.ConcurrentHashMap<String, java.util.concurrent.atomic.AtomicInteger> matches = new java.util.concurrent.ConcurrentHashMap<>();
  private final java.util.concurrent.ConcurrentHashMap<Class<?>, java.util.concurrent.atomic.AtomicReference<byte[]>> sources = new java.util.concurrent.ConcurrentHashMap<>();
  private final CallbackIndex callbacks = new CallbackIndex();

  void configure(List<ObserverAdapter> selected, boolean active) {
    adapters = new ArrayList<>(selected);
    enabled = active;
    failure = null;
    Set<String> ids = new HashSet<>();
    for (ObserverAdapter adapter : selected) ids.add(adapter.id());
    matches.keySet().retainAll(ids);
  }

  void check() {
    if (failure != null) throw new IllegalStateException("observer transformation failed", failure);
  }

  byte[] sourceBytes(java.lang.instrument.Instrumentation instrumentation, Class<?> type) throws Exception {
    java.util.concurrent.atomic.AtomicReference<byte[]> bytes = new java.util.concurrent.atomic.AtomicReference<>();
    if (sources.putIfAbsent(type, bytes) != null) throw new IllegalStateException("observer source capture already active for " + type.getName());
    try {
      instrumentation.retransformClasses(type);
      check();
      if (bytes.get() == null) throw new IllegalStateException("no class bytes received for " + type.getName());
      return bytes.get();
    } finally { sources.remove(type); }
  }

  int matched(String adapter) {
    java.util.concurrent.atomic.AtomicInteger count = matches.get(adapter);
    return count == null ? 0 : count.get();
  }

  void captureSource(Class<?> type, byte[] bytes) {
    java.util.concurrent.atomic.AtomicReference<byte[]> source = type == null ? null : sources.get(type);
    if (source != null) source.compareAndSet(null, bytes.clone());
  }

  boolean candidate(Class<?> type, List<ObserverAdapter> changed, boolean operationsChanged) {
    if (LoaderLifecycle.retired(type.getClassLoader()) != null) return false;
    String name = type.getName();
    if (excluded(name)) return false;
    if (operationsChanged && OtelOperations.isInstrumenter(name)) return true;
    Set<String> hierarchy = new HashSet<>();
    collect(type, hierarchy);
    for (ObserverAdapter adapter : changed) if (adapter.matchesType(name, hierarchy)) return true;
    return invocationCandidate(type, changed);
  }

  private boolean invocationCandidate(Class<?> type, List<ObserverAdapter> selected) {
    if (!hasInvocationAdapters(selected)) return false;
    Boolean known = callbacks.known(type);
    return known == null || known;
  }

  private boolean hasInvocationAdapters() {
    return hasInvocationAdapters(adapters);
  }

  private static boolean hasInvocationAdapters(List<ObserverAdapter> selected) {
    for (ObserverAdapter adapter : selected)
      if (adapter.matchesInvocation("javax.jms.MessageListener", "onMessage", "(Ljavax/jms/Message;)V")) return true;
    return false;
  }

  private static void collect(Class<?> type, Set<String> names) {
    if (type == null || !names.add(type.getName())) return;
    collect(type.getSuperclass(), names);
    for (Class<?> contract : type.getInterfaces()) collect(contract, names);
  }

  private static boolean excluded(String name) {
    return name.startsWith("org.objectweb.asm.")
        || name.startsWith("com.flanksource.telltale.shaded.")
        || name.startsWith("com.flanksource.telltale.observer.");
  }

  public byte[] transform(ClassLoader loader, String internal, Class<?> redefining,
      ProtectionDomain domain, byte[] bytes) {
    if (internal == null) return null;
    captureSource(redefining, bytes);
    String name = internal.replace('/', '.');
    if (excluded(name)) return null;
    try {
      boolean callback = callbacks.remember(loader, name, bytes);
      if (!enabled || LoaderLifecycle.retired(loader) != null) return null;
      ClassReader reader = new ClassReader(bytes);
      Set<String> hierarchy;
      if (redefining == null) hierarchy = hierarchy(loader, reader);
      else { hierarchy = new HashSet<>(); collect(redefining, hierarchy); }
      List<ObserverAdapter> matching = new ArrayList<>();
      for (ObserverAdapter adapter : adapters)
        if (adapter.matchesType(name, hierarchy)) matching.add(adapter);
      boolean operations = OtelOperations.isInstrumenter(name);
      boolean invocations = hasInvocationAdapters() && callback;
      if (matching.isEmpty() && !operations && !invocations) return null;
      boolean mappedFrames = reader.readUnsignedShort(6) >= Opcodes.V1_7;
      ClassWriter writer = new ClassWriter(reader, ClassWriter.COMPUTE_MAXS);
      Set<String> invocationMethods = invocations ? invocationMethods(reader) : Collections.emptySet();
      boolean[] changed = {false};
      reader.accept(new ClassVisitor(Opcodes.ASM9, writer) {
        public MethodVisitor visitMethod(int access, String method, String descriptor,
            String signature, String[] exceptions) {
          MethodVisitor visitor = super.visitMethod(access, method, descriptor, signature, exceptions);
          if ((access & (Opcodes.ACC_ABSTRACT | Opcodes.ACC_NATIVE)) != 0) return visitor;
          List<String> selected = new ArrayList<>();
          for (ObserverAdapter adapter : matching) if (adapter.matchesMethod(method, descriptor)) selected.add(adapter.id());
          boolean calls = invocationMethods.contains(method + descriptor);
          if (selected.isEmpty() && !calls && !operations) return visitor;
          MethodFrames.Buffered buffered = new MethodFrames.Buffered(access, method, descriptor, signature, exceptions, visitor);
          MethodFrames frames = new MethodFrames(internal, access, method, descriptor, buffered, mappedFrames);
          visitor = frames;
          if (operations) {
            MethodVisitor operation = OtelOperations.weave(visitor, access, method, descriptor, internal, changed);
            if (operation != null) visitor = operation;
          }
          if (!selected.isEmpty() || calls) {
            CallAdvice advice = new CallAdvice(visitor, access, method, descriptor, selected, name, frames, buffered, calls);
            visitor = calls ? new InvocationVisitor(advice, adapters, changed) : advice;
          }
          for (String adapter : selected) {
            matches.computeIfAbsent(adapter, unused -> new java.util.concurrent.atomic.AtomicInteger()).incrementAndGet();
            changed[0] = true;
          }
          return visitor;
        }
      }, mappedFrames ? ClassReader.EXPAND_FRAMES : ClassReader.SKIP_FRAMES);
      if (!changed[0]) return null;
      return writer.toByteArray();
    } catch (Throwable error) {
      failure = new IllegalStateException("instrument " + name + ": " + error, error);
      ObserverRuntime.fail(null, failure);
      return null;
    }
  }

  private Set<String> invocationMethods(ClassReader reader) {
    Set<String> methods = new HashSet<>();
    reader.accept(new ClassVisitor(Opcodes.ASM9) {
      public MethodVisitor visitMethod(int access, String name, String descriptor, String signature, String[] exceptions) {
        return new MethodVisitor(Opcodes.ASM9) {
          public void visitMethodInsn(int opcode, String owner, String method, String target, boolean contract) {
            if (opcode != Opcodes.INVOKEINTERFACE) return;
            for (ObserverAdapter adapter : adapters)
              if (adapter.matchesInvocation(owner.replace('/', '.'), method, target)) methods.add(name + descriptor);
          }
        };
      }
    }, ClassReader.SKIP_DEBUG | ClassReader.SKIP_FRAMES);
    return methods;
  }

  private static Set<String> hierarchy(ClassLoader loader, ClassReader reader) throws IOException {
    Set<String> names = new HashSet<>();
    hierarchy(loader, reader, names);
    return names;
  }

  private static void hierarchy(ClassLoader loader, ClassReader reader, Set<String> names) throws IOException {
    if (!names.add(reader.getClassName().replace('/', '.'))) return;
    List<String> parents = new ArrayList<>(Arrays.asList(reader.getInterfaces()));
    if (reader.getSuperName() != null) parents.add(reader.getSuperName());
    for (String parent : parents) {
      if (names.contains(parent.replace('/', '.'))) continue;
      try (InputStream stream = loader == null ? ClassLoader.getSystemResourceAsStream(parent + ".class") : loader.getResourceAsStream(parent + ".class")) {
        if (stream == null) {
          names.add(parent.replace('/', '.'));
          continue;
        }
        hierarchy(loader, new ClassReader(stream), names);
      }
    }
  }

  private static final class CallAdvice extends AdviceAdapter {
    final List<String> adapters;
    final String owner, name, descriptor;
    final Type result;
    final boolean isStatic;
    final boolean invocations;
    final MethodFrames frames;
    final MethodFrames.Buffered buffered;
    final Label start = new Label(), end = new Label();
    int tokens, arguments, value, error, tokenSlot;
    int callbackArgs, callbackReceiver, callbackToken, callbackValue, callbackError;
    boolean initialized;
    CallAdvice(MethodVisitor delegate, int access, String name, String descriptor, List<String> adapters, String owner,
        MethodFrames frames, MethodFrames.Buffered buffered, boolean invocations) {
      super(Opcodes.ASM9, delegate, access, name, descriptor);
      this.adapters = adapters; this.owner = owner; this.name = name; this.descriptor = descriptor;
      this.frames = frames; this.buffered = buffered; this.invocations = invocations;
      result = Type.getReturnType(descriptor); isStatic = (access & Opcodes.ACC_STATIC) != 0;
    }
    public void visitCode() { super.visitCode(); initialize(); }
    private int local(Type type) {
      int variable = newLocal(type);
      switch (type.getSort()) {
        case Type.LONG: push(0L); break;
        case Type.DOUBLE: push(0d); break;
        case Type.FLOAT: push(0f); break;
        case Type.BOOLEAN: case Type.BYTE: case Type.CHAR: case Type.SHORT: case Type.INT: push(0); break;
        default: nil();
      }
      storeLocal(variable);
      Object frameType = type.getSort() >= Type.ARRAY ? type.getInternalName()
          : type.getSort() == Type.LONG ? Opcodes.LONG : type.getSort() == Type.DOUBLE ? Opcodes.DOUBLE
          : type.getSort() == Type.FLOAT ? Opcodes.FLOAT : Opcodes.INTEGER;
      frames.hookLocals.put(frames.lastStore, frameType);
      if (type.getSize() == 2) frames.hookLocals.put(frames.lastStore + 1, Opcodes.TOP);
      return variable;
    }
    private void nil() { mv.visitInsn(ACONST_NULL); }
    private void initialize() {
      if (initialized) return;
      initialized = true;
      if (!adapters.isEmpty()) {
        tokens = local(Type.getType(Object[].class)); tokenSlot = frames.lastStore;
        arguments = local(Type.getType(Object[].class)); error = local(Type.getType(Throwable.class));
        if (result.getSort() != Type.VOID) value = local(result);
      }
      if (invocations) {
        callbackArgs = local(Type.getType(Object[].class)); callbackReceiver = local(Type.getType(Object.class));
        callbackToken = local(Type.getType(Object.class)); callbackValue = local(Type.getType(Object.class));
        callbackError = local(Type.getType(Throwable.class));
      }
    }
    protected void onMethodEnter() {
      initialize();
      if (adapters.isEmpty()) return;
      push(adapters.size()); newArray(Type.getType(Object.class)); storeLocal(tokens);
      loadArgArray(); storeLocal(arguments);
      for (int index = 0; index < adapters.size(); index++) {
        loadLocal(tokens); push(index); push(adapters.get(index)); push(owner); push(name); push(descriptor);
        if (isStatic) nil(); else loadThis();
        loadLocal(arguments); invokeStatic(BRIDGE, ENTER); arrayStore(Type.getType(Object.class));
      }
      Type[] args = Type.getArgumentTypes(descriptor);
      for (int i = 0; i < args.length; i++) {
        loadLocal(arguments); push(i); arrayLoad(Type.getType(Object.class)); unbox(args[i]); storeArg(i);
      }
      mark(start);
    }
    protected void onMethodExit(int opcode) {
      if (opcode == ATHROW || adapters.isEmpty()) return;
      if (result.getSort() != Type.VOID) storeLocal(value);
      for (int index = adapters.size() - 1; index >= 0; index--) {
        loadLocal(tokens); push(index); arrayLoad(Type.getType(Object.class));
        if (result.getSort() == Type.VOID) nil(); else { loadLocal(value); box(result); }
        nil(); invokeStatic(BRIDGE, EXIT);
        if (result.getSort() == Type.VOID) pop(); else { unbox(result); storeLocal(value); }
      }
      if (result.getSort() != Type.VOID) loadLocal(value);
    }
    public void visitMaxs(int maxStack, int maxLocals) {
      if (!adapters.isEmpty()) {
        mark(end); Label handler = new Label();
        mv.visitTryCatchBlock(start, end, handler, "java/lang/Throwable");
        buffered.methods.add(handler); mark(handler); frames.handler(tokenSlot); storeLocal(error);
        for (int index = adapters.size() - 1; index >= 0; index--) {
          loadLocal(tokens); push(index); arrayLoad(Type.getType(Object.class));
          nil(); loadLocal(error); invokeStatic(BRIDGE, EXIT); pop();
        }
        loadLocal(error); throwException();
      }
      super.visitMaxs(maxStack, maxLocals);
    }
    void callback(ObserverAdapter adapter, int opcode, String owner, String name, String descriptor, boolean contract) {
      Type[] args = Type.getArgumentTypes(descriptor);
      push(args.length); newArray(Type.getType(Object.class)); storeLocal(callbackArgs);
      for (int index = args.length - 1; index >= 0; index--) {
        box(args[index]); storeLocal(callbackValue);
        loadLocal(callbackArgs); push(index); loadLocal(callbackValue); arrayStore(Type.getType(Object.class));
      }
      storeLocal(callbackReceiver);
      push(adapter.id()); push(owner.replace('/', '.')); push(name); push(descriptor); loadLocal(callbackReceiver); loadLocal(callbackArgs);
      invokeStatic(BRIDGE, ENTER); storeLocal(callbackToken);
      MethodFrames.Snapshot before = frames.snapshot();
      Label begin = new Label(), finish = new Label(), handler = new Label(), done = new Label();
      mv.visitTryCatchBlock(begin, finish, handler, "java/lang/Throwable"); buffered.callbacks.add(handler);
      mark(begin); loadLocal(callbackReceiver); checkCast(Type.getObjectType(owner));
      for (int index = 0; index < args.length; index++) {
        loadLocal(callbackArgs); push(index); arrayLoad(Type.getType(Object.class)); unbox(args[index]);
      }
      visitMethodInsn(opcode, owner, name, descriptor, contract);
      mark(finish);
      Type callbackResult = Type.getReturnType(descriptor);
      if (callbackResult.getSort() != Type.VOID) { box(callbackResult); storeLocal(callbackValue); }
      loadLocal(callbackToken);
      if (callbackResult.getSort() == Type.VOID) nil(); else loadLocal(callbackValue);
      nil(); invokeStatic(BRIDGE, EXIT);
      if (callbackResult.getSort() == Type.VOID) pop(); else unbox(callbackResult);
      MethodFrames.Snapshot after = frames.snapshot();
      goTo(done); mark(handler); if (before != null) frames.emit(before.handler()); storeLocal(callbackError);
      loadLocal(callbackToken); nil(); loadLocal(callbackError); invokeStatic(BRIDGE, EXIT); pop();
      loadLocal(callbackError); throwException(); mark(done); if (after != null) frames.emit(after);
    }
  }

  private static final class InvocationVisitor extends MethodVisitor {
    final CallAdvice advice;
    final List<ObserverAdapter> adapters;
    final boolean[] changed;
    InvocationVisitor(CallAdvice advice, List<ObserverAdapter> adapters, boolean[] changed) {
      super(Opcodes.ASM9, advice); this.advice = advice; this.adapters = adapters; this.changed = changed;
    }
    public void visitMethodInsn(int opcode, String owner, String name, String descriptor, boolean contract) {
      if (opcode == Opcodes.INVOKEINTERFACE) for (ObserverAdapter adapter : adapters) {
        if (!adapter.matchesInvocation(owner.replace('/', '.'), name, descriptor)) continue;
        changed[0] = true; advice.callback(adapter, opcode, owner, name, descriptor, contract); return;
      }
      super.visitMethodInsn(opcode, owner, name, descriptor, contract);
    }
  }
}
