package com.flanksource.telltale.diag.agent;

import java.lang.reflect.InvocationTargetException;

/**
 * Loads {@link DiagAgent} into an already-running JVM from the outside.
 *
 * <p>This exists because the Attach API has two halves that must come from the same JDK: {@code
 * tools.jar} (the classes) and {@code libattach.so} (the native transport, found via {@code
 * java.library.path}). A JRE-only application image has neither, and pointing an in-process
 * classloader at a side-loaded {@code tools.jar} only supplies the first half — {@code
 * VirtualMachine.attach} then dies with {@code UnsatisfiedLinkError: no attach in
 * java.library.path}.
 *
 * <p>Running the attach in a child process launched from the side-loaded JDK's own {@code bin/java}
 * makes both halves line up, because that launcher puts its own {@code jre/lib/<arch>} on the
 * library path. It also sidesteps the Java 9+ {@code jdk.attach.allowAttachSelf} restriction: from
 * here the target is another process, not this one.
 *
 * <p>Driven reflectively so this class compiles without {@code tools.jar} on the build's classpath;
 * the launcher supplies it via {@code -cp}.
 *
 * <p>Usage: {@code java -cp <tools.jar>:<agent.jar> com.flanksource.telltale.diag.agent.AttachMain
 * <pid> <agent.jar>}
 */
public final class AttachMain {

  private static final String VIRTUAL_MACHINE = "com.sun.tools.attach.VirtualMachine";

  private AttachMain() {}

  public static void main(String[] args) {
    if (args.length != 2) {
      System.err.println("usage: AttachMain <pid> <agent.jar>");
      System.exit(2);
      return;
    }
    try {
      Class<?> vmClass = Class.forName(VIRTUAL_MACHINE);
      Object vm = vmClass.getMethod("attach", String.class).invoke(null, args[0]);
      try {
        vmClass.getMethod("loadAgent", String.class).invoke(vm, args[1]);
      } finally {
        detach(vmClass, vm);
      }
      System.out.println("attached " + args[1] + " to pid " + args[0]);
    } catch (InvocationTargetException e) {
      // The interesting failure is always the cause: AttachNotSupportedException,
      // AgentLoadException, IOException from the attach socket.
      print(e.getCause() == null ? e : e.getCause());
      System.exit(1);
    } catch (Throwable t) {
      print(t);
      System.exit(1);
    }
  }

  private static void detach(Class<?> vmClass, Object vm) {
    try {
      vmClass.getMethod("detach").invoke(vm);
    } catch (ReflectiveOperationException | RuntimeException ignored) {
      // The agent is already loaded; a failed detach must not mask that.
    }
  }

  private static void print(Throwable t) {
    System.err.println(t.getClass().getName() + ": " + t.getMessage());
    t.printStackTrace();
  }
}
