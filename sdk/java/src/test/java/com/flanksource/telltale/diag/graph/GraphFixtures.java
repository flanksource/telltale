package com.flanksource.telltale.diag.graph;

import java.util.AbstractList;
import java.util.Arrays;
import java.util.Iterator;
import java.util.List;
import org.objectweb.asm.ClassWriter;
import org.objectweb.asm.MethodVisitor;
import org.objectweb.asm.Opcodes;

/**
 * The values {@link ValueGraphTest} walks. Most count every call into their own code, which a probe
 * must never make.
 */
final class GraphFixtures {

  private GraphFixtures() {}

  static String repeat(char c, int count) {
    char[] out = new char[count];
    Arrays.fill(out, c);
    return new String(out);
  }

  static Policy policy(String number) {
    Policy policy = new Policy();
    policy.policyNumber = number;
    policy.plan = new Plan();
    policy.plan.name = "Preservation";
    return policy;
  }

  static Activity activity() {
    Activity activity = new Activity();
    activity.activityGuid = "ACT-1";
    activity.statusCode = "02";
    activity.policy = policy("P-1");
    return activity;
  }

  /**
   * A class whose only field is of a type its loader cannot find, defined through a loader with no
   * parent.
   */
  static Object holderWithAMissingFieldType() throws Exception {
    final String name = "com/flanksource/telltale/diag/graph/fixture/Holder";
    ClassWriter writer = new ClassWriter(ClassWriter.COMPUTE_MAXS);
    writer.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, name, null, "java/lang/Object", null);
    writer
        .visitField(
            Opcodes.ACC_PRIVATE,
            "missing",
            "Lcom/flanksource/telltale/diag/graph/fixture/Missing;",
            null,
            null)
        .visitEnd();
    MethodVisitor init = writer.visitMethod(Opcodes.ACC_PUBLIC, "<init>", "()V", null, null);
    init.visitCode();
    init.visitVarInsn(Opcodes.ALOAD, 0);
    init.visitMethodInsn(Opcodes.INVOKESPECIAL, "java/lang/Object", "<init>", "()V", false);
    init.visitInsn(Opcodes.RETURN);
    init.visitMaxs(0, 0);
    init.visitEnd();
    writer.visitEnd();
    final byte[] bytes = writer.toByteArray();
    Class<?> holder =
        new ClassLoader(null) {
          Class<?> define() {
            return defineClass(name.replace('/', '.'), bytes, 0, bytes.length);
          }
        }.define();
    return holder.getDeclaredConstructor().newInstance();
  }

  static final class Cycle {
    Cycle self;
  }

  static final class Plan {
    String name;
  }

  static final class Policy {
    String policyNumber;
    Plan plan;
  }

  /** Counts every call into its own code, which a probe snapshot must never make. */
  static final class Activity {
    String activityGuid;
    String statusCode;
    Policy policy;
    int calls;

    public String getActivityGuid() {
      calls++;
      throw new AssertionError("a getter ran");
    }

    @Override
    public String toString() {
      calls++;
      throw new AssertionError("toString ran");
    }

    @Override
    public int hashCode() {
      calls++;
      throw new AssertionError("hashCode ran");
    }

    @Override
    public boolean equals(Object other) {
      calls++;
      throw new AssertionError("equals ran");
    }
  }

  /** Stands in for Hibernate's PersistentBag: iterating it would load it. */
  static final class PersistentBag extends AbstractList<Object> {
    boolean initialized;
    List<Object> bag;
    int touched;

    @Override
    public Object get(int index) {
      touched++;
      throw new AssertionError("loaded");
    }

    @Override
    public int size() {
      touched++;
      throw new AssertionError("loaded");
    }

    @Override
    public Iterator<Object> iterator() {
      touched++;
      throw new AssertionError("loaded");
    }
  }

  enum Status {
    PENDING {
      @Override
      public String toString() {
        return "overridden";
      }
    }
  }

  /** An application CharSequence, counting every call into it. */
  static final class Sequence implements CharSequence {
    int calls;

    @Override
    public int length() {
      calls++;
      return 3;
    }

    @Override
    public char charAt(int index) {
      calls++;
      return 'x';
    }

    @Override
    public CharSequence subSequence(int start, int end) {
      calls++;
      return "xxx";
    }

    @Override
    public String toString() {
      calls++;
      return "xxx";
    }
  }

  static final class LoudException extends RuntimeException {
    int calls;

    LoudException(String message) {
      super(message);
    }

    @Override
    public String getMessage() {
      calls++;
      return "overridden";
    }
  }
}
