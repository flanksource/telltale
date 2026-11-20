package com.flanksource.telltale.observer;

import static org.junit.Assert.*;
import java.io.*;
import java.lang.reflect.*;
import java.net.*;
import java.util.*;
import javax.jms.MessageListener;
import net.bytebuddy.agent.ByteBuddyAgent;
import observerfixture.FrameParent;
import observerfixture.FrameSupplier;
import observerfixture.closedframes.Choice;
import org.junit.Before;
import org.junit.Test;
import org.objectweb.asm.*;

public class FramePreservationTest {
  private static final long VALUE = 9007199254740993L;
  @Before public void attach() throws Exception {
    ObserverRuntime.install(ByteBuddyAgent.install());
    ObserverRuntime.register(new Adapter());
  }

  @Test public void keepsOriginalFramesWhenAClosedLoaderCannotResolveAnUnusedBranchType() throws Exception {
    try (ClosedLoader loader = new ClosedLoader()) {
      Class<?> type = loader.loadClass("observerfixture.closedframes.Choice");
      Object target = type.getConstructor().newInstance();
      Method choose = type.getMethod("choose", boolean.class, long.class);
      Object warm = choose.invoke(target, false, VALUE);
      assertFalse("the branch helper must remain unloaded", loader.loaded("observerfixture.closedframes.Unused"));
      loader.close();
      try (ObserverSubscription capture = subscribe("closed-branch")) {
        Object result = choose.invoke(target, false, VALUE);
        assertEquals(warm.getClass(), result.getClass());
        assertEquals(VALUE, result.getClass().getField("value").getLong(result));
        assertEquals(Collections.singletonList(row("choose", "")), capture.drain());
      }
    }
  }

  @Test public void preservesWideOperandPrefixesAndTheApplicationsOriginalExceptionHandler() throws Exception {
    Class<?> generated = new GeneratedLoader().define("observerfixture.WideCallback", dispatcher());
    Method dispatch = generated.getMethod("dispatch", long.class, MessageListener.class, javax.jms.Message.class);
    MessageListener normal = message -> {};
    assertEquals(VALUE, dispatch.invoke(null, VALUE, normal, null));
    IllegalArgumentException failure = new IllegalArgumentException("handled callback failure");
    MessageListener failing = message -> { throw failure; };
    try (ObserverSubscription capture = subscribe("wide-callback")) {
      assertEquals(VALUE, dispatch.invoke(null, VALUE, normal, null));
      assertEquals(-VALUE, dispatch.invoke(null, VALUE, failing, null));
      assertEquals(Arrays.asList(row("onMessage", ""), row("onMessage", failure.getMessage())), capture.drain());
    }
  }

  @Test public void preservesPreSuperCallbackFramesAndNonvoidResultsDuringConstructorInitialization() throws Exception {
    Class<?> generated = new GeneratedLoader().define("observerfixture.ConstructorCallback", constructor());
    Constructor<?> constructor = generated.getConstructor(FrameSupplier.class);
    FrameSupplier normal = () -> VALUE;
    assertEquals(VALUE, ((FrameParent) constructor.newInstance(normal)).value);
    IllegalStateException failure = new IllegalStateException("constructor callback failure");
    FrameSupplier failing = () -> { throw failure; };
    try (ObserverSubscription capture = subscribe("constructor-callback")) {
      assertEquals(VALUE, ((FrameParent) constructor.newInstance(normal)).value);
      try { constructor.newInstance(failing); fail("the original callback failure must propagate"); }
      catch (InvocationTargetException expected) { assertSame(failure, expected.getCause()); }
      assertEquals(Arrays.asList(row("onMessage", ""), row("value", ""),
          row("onMessage", ""), row("value", failure.getMessage())), capture.drain());
    }
  }

  private static ObserverSubscription subscribe(String id) throws Exception {
    return ObserverRuntime.subscribe("preserved-frames", id, Collections.emptyMap());
  }
  private static Map<String, Object> row(String method, String error) {
    Map<String, Object> row = new LinkedHashMap<>(); row.put("method", method); row.put("error", error); return row;
  }

  private static byte[] dispatcher() {
    ClassWriter writer = new ClassWriter(ClassWriter.COMPUTE_FRAMES | ClassWriter.COMPUTE_MAXS);
    writer.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, "observerfixture/WideCallback", null, "java/lang/Object", null);
    MethodVisitor method = writer.visitMethod(Opcodes.ACC_PUBLIC | Opcodes.ACC_STATIC, "dispatch",
        "(JLjavax/jms/MessageListener;Ljavax/jms/Message;)J", null, null);
    method.visitCode(); Label start = new Label(), end = new Label(), caught = new Label();
    method.visitTryCatchBlock(start, end, caught, "java/lang/IllegalArgumentException"); method.visitLabel(start);
    method.visitVarInsn(Opcodes.LLOAD, 0); method.visitVarInsn(Opcodes.ALOAD, 2); method.visitVarInsn(Opcodes.ALOAD, 3);
    method.visitMethodInsn(Opcodes.INVOKEINTERFACE, "javax/jms/MessageListener", "onMessage", "(Ljavax/jms/Message;)V", true);
    method.visitInsn(Opcodes.LRETURN); method.visitLabel(end); method.visitLabel(caught);
    method.visitVarInsn(Opcodes.ASTORE, 4); method.visitVarInsn(Opcodes.LLOAD, 0); method.visitInsn(Opcodes.LNEG);
    method.visitInsn(Opcodes.LRETURN); method.visitMaxs(0, 0); method.visitEnd(); writer.visitEnd(); return writer.toByteArray();
  }

  private static byte[] constructor() {
    ClassWriter writer = new ClassWriter(ClassWriter.COMPUTE_FRAMES | ClassWriter.COMPUTE_MAXS);
    writer.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, "observerfixture/ConstructorCallback", null, "observerfixture/FrameParent", null);
    MethodVisitor method = writer.visitMethod(Opcodes.ACC_PUBLIC, "<init>", "(Lobserverfixture/FrameSupplier;)V", null, null);
    method.visitCode(); method.visitVarInsn(Opcodes.ALOAD, 0); method.visitVarInsn(Opcodes.ALOAD, 1);
    method.visitInsn(Opcodes.ACONST_NULL);
    method.visitMethodInsn(Opcodes.INVOKEINTERFACE, "javax/jms/MessageListener", "onMessage", "(Ljavax/jms/Message;)V", true);
    method.visitVarInsn(Opcodes.ALOAD, 1);
    method.visitMethodInsn(Opcodes.INVOKEINTERFACE, "observerfixture/FrameSupplier", "value", "()J", true);
    method.visitMethodInsn(Opcodes.INVOKESPECIAL, "observerfixture/FrameParent", "<init>", "(J)V", false);
    method.visitInsn(Opcodes.RETURN); method.visitMaxs(0, 0); method.visitEnd(); writer.visitEnd(); return writer.toByteArray();
  }

  private static final class GeneratedLoader extends ClassLoader {
    GeneratedLoader() { super(FramePreservationTest.class.getClassLoader()); }
    Class<?> define(String name, byte[] bytes) { return defineClass(name, bytes, 0, bytes.length); }
  }
  private static final class ClosedLoader extends URLClassLoader {
    ClosedLoader() {
      super(new URL[] {Choice.class.getProtectionDomain().getCodeSource().getLocation()}, FramePreservationTest.class.getClassLoader());
    }
    boolean loaded(String name) { return findLoadedClass(name) != null; }
    protected Class<?> loadClass(String name, boolean resolve) throws ClassNotFoundException {
      if (!name.startsWith("observerfixture.closedframes.")) return super.loadClass(name, resolve);
      synchronized (getClassLoadingLock(name)) {
        Class<?> type = findLoadedClass(name); if (type == null) type = findClass(name);
        if (resolve) resolveClass(type); return type;
      }
    }
    public InputStream getResourceAsStream(String name) {
      if (!name.startsWith("observerfixture/closedframes/")) return super.getResourceAsStream(name);
      URL resource = findResource(name);
      if (resource == null) return null;
      try { return resource.openStream(); }
      catch (IOException error) { throw new IllegalStateException("read frame fixture " + name, error); }
    }
  }
  private static final class Adapter implements ObserverAdapter {
    public String id() { return "preserved-frames"; }
    public Set<String> kinds() { return Collections.singleton("preserved-frames"); }
    public boolean matchesType(String name, Set<String> hierarchy) { return name.equals("observerfixture.closedframes.Choice"); }
    public boolean matchesMethod(String name, String descriptor) { return name.equals("choose"); }
    public boolean matchesInvocation(String owner, String name, String descriptor) {
      return owner.equals("javax.jms.MessageListener") && name.equals("onMessage")
          || owner.equals("observerfixture.FrameSupplier") && name.equals("value");
    }
    public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) { return name; }
    public Object onExit(Object token, Object result, Throwable error) {
      ObserverRuntime.emit("preserved-frames", row(token.toString(), error == null ? "" : error.getMessage())); return result;
    }
  }
}
