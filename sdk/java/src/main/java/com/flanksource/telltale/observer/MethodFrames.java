package com.flanksource.telltale.observer;

import java.util.*;
import org.objectweb.asm.*;
import org.objectweb.asm.commons.AnalyzerAdapter;
import org.objectweb.asm.tree.*;

final class MethodFrames extends MethodVisitor {
  private final AnalyzerAdapter analyzer;
  final Map<Integer, Object> hookLocals = new LinkedHashMap<>();
  int lastStore;

  MethodFrames(String owner, int access, String name, String descriptor, MethodVisitor delegate, boolean mapped) {
    super(Opcodes.ASM9, mapped ? new AnalyzerAdapter(owner, access, name, descriptor, delegate) : delegate);
    analyzer = mapped ? (AnalyzerAdapter) mv : null;
  }

  public void visitVarInsn(int opcode, int variable) {
    super.visitVarInsn(opcode, variable);
    if (opcode >= Opcodes.ISTORE && opcode <= Opcodes.ASTORE) lastStore = variable;
  }

  Snapshot snapshot() {
    if (analyzer == null) return null;
    if (analyzer.locals == null || analyzer.stack == null)
      throw new IllegalStateException("callback has no verifier state");
    return new Snapshot(new ArrayList<>(analyzer.locals), new ArrayList<>(analyzer.stack));
  }

  void emit(Snapshot snapshot) {
    if (analyzer == null) return;
    List<Object> locals = new ArrayList<>(snapshot.locals);
    for (Map.Entry<Integer, Object> local : hookLocals.entrySet()) {
      while (locals.size() <= local.getKey()) locals.add(Opcodes.TOP);
      locals.set(local.getKey(), local.getValue());
    }
    Object[] localTypes = compact(locals), stackTypes = compact(snapshot.stack);
    mv.visitFrame(Opcodes.F_NEW, localTypes.length, localTypes, stackTypes.length, stackTypes);
  }

  void handler(int tokenSlot) {
    if (analyzer == null) return;
    List<Object> locals = new ArrayList<>(Collections.nCopies(tokenSlot + 1, Opcodes.TOP));
    locals.set(tokenSlot, "[Ljava/lang/Object;");
    Object[] localTypes = compact(locals);
    mv.visitFrame(Opcodes.F_NEW, localTypes.length, localTypes, 1, new Object[] {"java/lang/Throwable"});
  }

  private static Object[] compact(List<Object> slots) {
    List<Object> entries = new ArrayList<>();
    for (int index = 0; index < slots.size(); index++) {
      Object value = slots.get(index);
      entries.add(value);
      if (Objects.equals(value, Opcodes.LONG) || Objects.equals(value, Opcodes.DOUBLE)) {
        if (++index >= slots.size() || !Objects.equals(slots.get(index), Opcodes.TOP))
          throw new IllegalStateException("invalid category-two verifier slots");
      }
    }
    while (!entries.isEmpty() && Objects.equals(entries.get(entries.size() - 1), Opcodes.TOP)) entries.remove(entries.size() - 1);
    return entries.toArray();
  }

  static final class Snapshot {
    final List<Object> locals, stack;
    Snapshot(List<Object> locals, List<Object> stack) { this.locals = locals; this.stack = stack; }
    Snapshot handler() { return new Snapshot(locals, Collections.singletonList("java/lang/Throwable")); }
  }

  static final class Buffered extends MethodNode {
    private final MethodVisitor target;
    final Set<Label> callbacks = Collections.newSetFromMap(new IdentityHashMap<>());
    final Set<Label> methods = Collections.newSetFromMap(new IdentityHashMap<>());
    Buffered(int access, String name, String descriptor, String signature, String[] exceptions, MethodVisitor target) {
      super(Opcodes.ASM9, access, name, descriptor, signature, exceptions); this.target = target;
    }
    public void visitEnd() {
      FrameNode previous = null;
      for (AbstractInsnNode instruction = instructions.getFirst(); instruction != null;) {
        AbstractInsnNode next = instruction.getNext();
        if (instruction instanceof FrameNode) {
          if (previous != null) instructions.remove(previous);
          previous = (FrameNode) instruction;
        } else if (instruction.getOpcode() >= 0) previous = null;
        instruction = next;
      }
      tryCatchBlocks.sort(Comparator.comparingInt(block -> priority(block.handler)));
      accept(target);
    }
    private int priority(LabelNode handler) {
      for (Label label : callbacks) if (label.info == handler) return 0;
      for (Label label : methods) if (label.info == handler) return 2;
      return 1;
    }
  }
}
