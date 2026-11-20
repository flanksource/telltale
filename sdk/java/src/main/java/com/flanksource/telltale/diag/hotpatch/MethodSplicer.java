package com.flanksource.telltale.diag.hotpatch;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.NoSuchElementException;
import org.objectweb.asm.ClassReader;
import org.objectweb.asm.ClassVisitor;
import org.objectweb.asm.ClassWriter;
import org.objectweb.asm.MethodVisitor;
import org.objectweb.asm.Opcodes;
import org.objectweb.asm.commons.Method;
import org.objectweb.asm.tree.ClassNode;
import org.objectweb.asm.tree.MethodNode;

/**
 * Puts one freshly compiled method into a class's live bytes, leaving every other method exactly as
 * it was running.
 *
 * <p>This is what replaces "decompile the class, edit the text, recompile the whole thing". A
 * whole-class recompile discards anything an APM or tracing agent had woven into the other methods
 * — on the very class someone is debugging — and depends on a decompilation being faithful enough
 * to compile at all. Here the author's method is compiled against a generated skeleton (see {@link
 * SkeletonService}), and only its bytecode crosses over.
 */
public final class MethodSplicer {

  /**
   * The class-file method access flags. ASM adds {@code ACC_DEPRECATED} (0x20000) of its own when
   * the attribute is present, which is not a modifier and must not be compared as one.
   */
  private static final int JVM_METHOD_FLAGS = 0xFFFF;

  /**
   * @param liveBytecode what the class is currently running.
   * @param compiled every class javac produced from the skeleton, keyed by binary name.
   * @param loader the loader that defined the class, used to compute frames.
   * @return the live bytes with {@code target}'s body replaced.
   */
  public byte[] splice(
      String className,
      byte[] liveBytecode,
      Map<String, byte[]> compiled,
      Method target,
      ClassLoader loader) {
    final MethodNode replacement = methodFrom(compiled, className, target);
    ClassReader reader = new ClassReader(liveBytecode);
    ClassWriter writer = new TargetLoaderClassWriter(reader, loader);
    final boolean[] spliced = {false};

    byte[] patched;
    try {
      // Frames are computed as the instructions are visited, so the
      // resolution this can fail on happens here, not in toByteArray.
      reader.accept(
          new ClassVisitor(Opcodes.ASM9, writer) {
            @Override
            public MethodVisitor visitMethod(
                int access, String name, String descriptor, String signature, String[] exceptions) {
              if (!target.getName().equals(name) || !target.getDescriptor().equals(descriptor)) {
                // Unwrapped, so ASM copies this method's bytes verbatim.
                return super.visitMethod(access, name, descriptor, signature, exceptions);
              }
              refuseModifierChange(className, target, access, replacement.access);
              spliced[0] = true;
              // accept() writes the whole method — declaration and body — so
              // the original is skipped entirely by returning null below.
              replacement.accept(cv);
              return null;
            }
          },
          ClassReader.SKIP_FRAMES);
      patched = writer.toByteArray();
    } catch (TypeNotPresentException e) {
      throw new IllegalStateException(
          "computing stack map frames for "
              + className
              + "."
              + target.getName()
              + target.getDescriptor()
              + " needs "
              + e.typeName()
              + ", which its own class loader cannot resolve",
          e);
    }

    if (!spliced[0]) {
      throw new NoSuchElementException(
          className
              + " no longer declares "
              + target.getName()
              + target.getDescriptor()
              + ", so there is nothing to splice into");
    }
    return patched;
  }

  /**
   * HotSpot rejects a redefinition that changes a modifier, and does so with a bare {@code
   * UnsupportedOperationException}. Naming the two modifier sets is the difference between "rewrite
   * the declaration" and a dead end.
   */
  private static void refuseModifierChange(
      String className, Method target, int live, int replacement) {
    if ((live & JVM_METHOD_FLAGS) == (replacement & JVM_METHOD_FLAGS)) {
      return;
    }
    throw new IllegalArgumentException(
        "the replacement declares "
            + className
            + "."
            + target.getName()
            + " with different modifiers than the running method ("
            + modifiers(replacement)
            + " vs "
            + modifiers(live)
            + "). A redefinition may change a method's body only,"
            + " so the declaration must keep the modifiers it has");
  }

  /**
   * Reads the one method out of the compiled skeleton, refusing anything the JVM could not then
   * accept.
   *
   * <p>A redefinition may change method bodies and nothing else, so a compile that produced a
   * second class — an anonymous class in the author's body — or a synthetic method — a lambda, or
   * access to another class's private member — cannot be applied. Saying so here names the
   * construct; letting the JVM say it produces an {@code UnsupportedOperationException} with no
   * hint of which one did it.
   */
  private static MethodNode methodFrom(
      Map<String, byte[]> compiled, String className, Method target) {
    byte[] compiledBytes = compiled.get(className);
    if (compiledBytes == null) {
      throw new IllegalArgumentException(
          "the compile produced no class named "
              + className
              + " (produced "
              + compiled.keySet()
              + ")");
    }
    List<String> newClasses = new ArrayList<String>(compiled.keySet());
    newClasses.remove(className);
    if (!newClasses.isEmpty()) {
      Collections.sort(newClasses);
      throw new IllegalStateException(
          "the replacement body for "
              + className
              + "."
              + target.getName()
              + " compiled to extra classes the JVM cannot add to a running system: "
              + newClasses
              + ". A redefinition may change method bodies only — an anonymous inner class in the"
              + " replacement is the usual cause; a named nested class that already exists is"
              + " fine");
    }

    ClassNode parsed = new ClassNode(Opcodes.ASM9);
    new ClassReader(compiledBytes).accept(parsed, ClassReader.SKIP_FRAMES);

    MethodNode found = null;
    List<String> synthetic = new ArrayList<String>();
    for (MethodNode method : parsed.methods) {
      if (target.getName().equals(method.name) && target.getDescriptor().equals(method.desc)) {
        found = method;
      } else if ((method.access & Opcodes.ACC_SYNTHETIC) != 0) {
        synthetic.add(method.name + method.desc);
      }
    }
    if (found == null) {
      throw new IllegalArgumentException(
          "the compiled source does not declare "
              + target.getName()
              + target.getDescriptor()
              + " of "
              + className
              + ", so there is nothing to splice — the replacement must declare the method being"
              + " replaced exactly, including its parameter types");
    }
    if (!synthetic.isEmpty()) {
      Collections.sort(synthetic);
      throw new IllegalStateException(
          "the replacement body for "
              + className
              + "."
              + target.getName()
              + " needs synthetic methods the JVM cannot add to a loaded class: "
              + synthetic
              + ". A redefinition may change method bodies only — rewrite the body without the"
              + " construct that needed them (a lambda, or access to a private member of another"
              + " class)");
    }
    return found;
  }

  /** Renders method access flags the way they would be written in Java. */
  private static String modifiers(int access) {
    StringBuilder out = new StringBuilder();
    append(out, access, Opcodes.ACC_PUBLIC, "public");
    append(out, access, Opcodes.ACC_PROTECTED, "protected");
    append(out, access, Opcodes.ACC_PRIVATE, "private");
    append(out, access, Opcodes.ACC_STATIC, "static");
    append(out, access, Opcodes.ACC_ABSTRACT, "abstract");
    append(out, access, Opcodes.ACC_FINAL, "final");
    append(out, access, Opcodes.ACC_SYNCHRONIZED, "synchronized");
    append(out, access, Opcodes.ACC_NATIVE, "native");
    append(out, access, Opcodes.ACC_STRICT, "strictfp");
    return out.length() == 0 ? "package-private" : out.toString();
  }

  private static void append(StringBuilder out, int access, int flag, String keyword) {
    if ((access & flag) != 0) {
      out.append(out.length() == 0 ? "" : " ").append(keyword);
    }
  }
}
