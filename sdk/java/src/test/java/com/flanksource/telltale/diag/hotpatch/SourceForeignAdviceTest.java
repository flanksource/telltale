package com.flanksource.telltale.diag.hotpatch;

import static org.junit.Assert.*;

import com.flanksource.telltale.diag.agent.DiagAgent;
import java.io.*;
import java.lang.instrument.*;
import java.lang.reflect.Method;
import java.security.ProtectionDomain;
import java.security.CodeSource;
import java.security.cert.Certificate;
import java.util.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.Test;
import org.objectweb.asm.*;
import org.objectweb.asm.commons.AdviceAdapter;

public class SourceForeignAdviceTest {
  private static final String CLIENT = "framefixture.ForeignSourceClient";

  @Test public void rejectsUncoordinatedSameMethodAdviceBeforeChangingApplicationCode() throws Exception {
    Instrumentation instrumentation = ByteBuddyAgent.install();
    DiagAgent.INSTRUMENTATION = instrumentation;
    ClassFileTransformer foreign = new ForeignAdvice();
    instrumentation.addTransformer(foreign, false);
    Class<?> client = new FixtureLoader(true).define();
    Object receiver = client.getConstructor().newInstance();
    Method request = client.getMethod("request", String.class);
    try {
      assertEquals("original:warm", request.invoke(receiver, "warm"));
      assertEquals(1, client.getField("foreignCalls").getInt(null));
      try { new ClassOperations().patch(options(client)); fail("uncoordinated foreign advice was overwritten"); }
      catch (IllegalStateException expected) {
        assertTrue(expected.getMessage(), expected.getMessage().contains(CLIENT + "#request(Ljava/lang/String;)Ljava/lang/String;"));
        assertTrue(expected.getMessage(), expected.getMessage().contains("uncoordinated"));
      }
      assertFalse(SourceTransformations.contains(client));
      assertEquals("original:after-rejection", request.invoke(receiver, "after-rejection"));
      assertEquals("foreign advice must still run after rejection", 2, client.getField("foreignCalls").getInt(null));
    } finally {
      if (SourceTransformations.contains(client)) SourceTransformations.restore(Collections.singletonList(client), null);
      instrumentation.removeTransformer(foreign);
    }
  }

  @Test public void rejectsAnUnverifiableCodeOriginBeforeChangingApplicationCode() throws Exception {
    DiagAgent.INSTRUMENTATION = ByteBuddyAgent.install();
    Class<?> client = new FixtureLoader(false).define();
    try { new ClassOperations().patch(options(client)); fail("unverifiable source edit was applied"); }
    catch (IllegalStateException expected) {
      assertTrue(expected.getMessage(), expected.getMessage().contains(CLIENT + "#request(Ljava/lang/String;)Ljava/lang/String;"));
      assertTrue(expected.getMessage(), expected.getMessage().contains("code-source"));
    }
    assertFalse(SourceTransformations.contains(client));
    Object receiver = client.getConstructor().newInstance();
    assertEquals("original:unchanged", client.getMethod("request", String.class).invoke(receiver, "unchanged"));
  }

  private static JsonBody options(Class<?> client) {
    Map<String, Object> options = new LinkedHashMap<>();
    options.put("class", CLIENT); options.put("loader", ClassIndex.Entry.of(client).getLoaderHash());
    options.put("method", "request(String)"); options.put("leaseSeconds", -1);
    options.put("source", "public String request(String value) { return \"patched:\" + value; }");
    return new JsonBody(options);
  }

  private static final class FixtureLoader extends ClassLoader {
    final boolean authoritative;
    FixtureLoader(boolean authoritative) { this.authoritative = authoritative; }
    Class<?> define() throws IOException {
      try (InputStream input = SourceForeignAdviceTest.class.getClassLoader().getResourceAsStream(CLIENT.replace('.', '/') + ".class")) {
        if (input == null) throw new FileNotFoundException(CLIENT);
        ByteArrayOutputStream output = new ByteArrayOutputStream(); byte[] buffer = new byte[8192];
        for (int count; (count = input.read(buffer)) != -1;) output.write(buffer, 0, count);
        byte[] bytes = output.toByteArray();
        if (!authoritative) return defineClass(CLIENT, bytes, 0, bytes.length);
        CodeSource source = new CodeSource(SourceForeignAdviceTest.class.getProtectionDomain().getCodeSource().getLocation(), (Certificate[]) null);
        return defineClass(CLIENT, bytes, 0, bytes.length, new ProtectionDomain(source, null, this, null));
      }
    }
  }

  private static final class ForeignAdvice implements ClassFileTransformer {
    public byte[] transform(ClassLoader loader, String name, Class<?> type, ProtectionDomain domain, byte[] bytes) {
      if (!CLIENT.replace('.', '/').equals(name)) return null;
      ClassReader reader = new ClassReader(bytes);
      ClassWriter writer = new ClassWriter(reader, ClassWriter.COMPUTE_MAXS);
      reader.accept(new ClassVisitor(Opcodes.ASM9, writer) {
        public MethodVisitor visitMethod(int access, String method, String descriptor, String signature, String[] exceptions) {
          MethodVisitor delegate = super.visitMethod(access, method, descriptor, signature, exceptions);
          if (!method.equals("request")) return delegate;
          return new AdviceAdapter(Opcodes.ASM9, delegate, access, method, descriptor) {
            protected void onMethodEnter() {
              String owner = CLIENT.replace('.', '/');
              visitFieldInsn(GETSTATIC, owner, "foreignCalls", "I"); visitInsn(ICONST_1);
              visitInsn(IADD); visitFieldInsn(PUTSTATIC, owner, "foreignCalls", "I");
            }
          };
        }
      }, ClassReader.EXPAND_FRAMES);
      return writer.toByteArray();
    }
  }
}
