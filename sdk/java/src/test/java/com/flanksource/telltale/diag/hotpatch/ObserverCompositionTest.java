package com.flanksource.telltale.diag.hotpatch;

import static org.junit.Assert.*;

import com.flanksource.telltale.diag.agent.DiagAgent;
import com.flanksource.telltale.observer.*;
import java.lang.instrument.Instrumentation;
import java.lang.instrument.ClassFileTransformer;
import java.security.ProtectionDomain;
import java.util.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.Test;
import org.objectweb.asm.*;
import org.objectweb.asm.commons.AdviceAdapter;

public class ObserverCompositionTest {
  public static final class Client {
    public static int foreignCalls;
    public String request(String value) { return "original:" + value; }
  }

  @Test
  public void keepsMethodProbesAcrossObserverArmingAndTeardownInBothOrders() throws Exception {
    Instrumentation instrumentation = ByteBuddyAgent.install();
    DiagAgent.INSTRUMENTATION = instrumentation;
    ObserverRuntime.install(instrumentation);
    registerAdapter();
    Client client = new Client();
    ClassOperations operations = new ClassOperations();
    ClassFileTransformer foreign = new ForeignTransformer();
    instrumentation.addTransformer(foreign, true);
    try {
    for (boolean observerFirst : new boolean[] {false, true}) {
      String key = "composition-probe-" + observerFirst;
      ObserverSubscription observation = observerFirst ? subscribe(key) : null;
      operations.instrument(body(key));
      if (observation == null) observation = subscribe(key);
      boolean probeClosed = false;
      try {
        int foreignBefore = Client.foreignCalls;
        assertEquals("original:active", client.request("active"));
        assertEquals(foreignBefore + 1, Client.foreignCalls);
        assertEquals(1, observation.drain().size());
        assertProbeRecorded(key, 1);
        if (observerFirst) {
          operations.restore(body(key));
          probeClosed = true;
          assertEquals("original:after-probe", client.request("after-probe"));
          assertEquals(1, observation.drain().size());
        } else {
          observation.close();
          assertEquals("original:after-observer", client.request("after-observer"));
          assertProbeRecorded(key, 2);
        }
        assertEquals(foreignBefore + 2, Client.foreignCalls);
      } finally {
        observation.close();
        if (!probeClosed) operations.restore(body(key));
      }
    }
    } finally { instrumentation.removeTransformer(foreign); instrumentation.retransformClasses(Client.class); }
  }

  @Test
  public void keepsSourceMethodRedefinitionAcrossObserverChangesAndRestoresWithoutDuplicateHooks() throws Exception {
    Instrumentation instrumentation = ByteBuddyAgent.install();
    DiagAgent.INSTRUMENTATION = instrumentation;
    ObserverRuntime.install(instrumentation);
    registerAdapter();
    ClassOperations operations = new ClassOperations();
    Client client = new Client();
    for (boolean observerFirst : new boolean[] {false, true}) {
      ObserverSubscription observation = observerFirst ? subscribe("source-" + observerFirst) : null;
      assertEquals("before source patch with observerFirst=" + observerFirst, "original:warm", client.request("warm"));
      if (observation != null) observation.drain();
      Map<String, Object> patch = new LinkedHashMap<>();
      patch.put("class", Client.class.getName()); patch.put("method", "request(String)");
      patch.put("loader", ClassIndex.Entry.of(Client.class).getLoaderHash());
      patch.put("source", "public String request(String value) { return \"patched:\" + value; }");
      patch.put("leaseSeconds", -1);
      assertEquals(true, operations.patch(new JsonBody(patch)).get("applied"));
      byte[] original = PatchService.originals().get(PatchService.key(
          ClassIndex.Entry.of(Client.class).getLoaderHash(), Client.class.getName()));
      Class<?> saved = LiveClass.define(Client.class.getName(), original);
      assertEquals("original:stash", saved.getMethod("request", String.class).invoke(saved.getConstructor().newInstance(), "stash"));
      if (observation != null) observation.drain();
      if (observation == null) observation = subscribe("source-" + observerFirst);
      try {
        assertEquals("patched:active", client.request("active"));
        assertEquals(1, observation.drain().size());
        if (observerFirst) {
          operations.restore(sourceBody());
          assertEquals("original:restored", client.request("restored"));
          assertEquals(1, observation.drain().size());
        }
        observation.close();
        assertEquals((observerFirst ? "original:" : "patched:") + "closed", client.request("closed"));
      } finally {
        observation.close();
        if (!observerFirst) operations.restore(sourceBody());
      }
      assertEquals("source restore with observerFirst=" + observerFirst, "original:final", client.request("final"));
    }
  }

  @Test public void sourceEditsAndMethodTracingRetainIndependentOwnershipOnTheSameObservedMethod() throws Exception {
    DiagAgent.INSTRUMENTATION = ByteBuddyAgent.install();
    ObserverRuntime.install(DiagAgent.INSTRUMENTATION); registerAdapter();
    ClassOperations operations = new ClassOperations(); Client client = new Client();
    for (boolean observerFirst : new boolean[] {false, true}) {
      String key = "coordinated-source-probe-" + observerFirst;
      ObserverSubscription capture = observerFirst ? subscribe(key) : null;
      operations.instrument(body(key));
      if (capture == null) capture = subscribe(key);
      Map<String, Object> source = new LinkedHashMap<>(sourceBody().fields());
      source.put("method", "request(String)"); source.put("leaseSeconds", -1);
      source.put("source", "public String request(String value) { return \"coordinated:\" + value; }");
      try {
        assertEquals(true, operations.patch(new JsonBody(source)).get("applied"));
        assertEquals("coordinated:active", client.request("active"));
        assertEquals(1, capture.drain().size()); assertTrue(probeEvents(key) > 0);
        source.put("source", "public String request(String value) { return \"updated:\" + value; }");
        assertEquals(true, operations.patch(new JsonBody(source)).get("applied"));
        int before = probeEvents(key);
        assertEquals("updated:active", client.request("active"));
        assertEquals(1, capture.drain().size()); assertTrue(probeEvents(key) > before);
        if (observerFirst) {
          operations.restore(new JsonBody(source));
          assertEquals(false, operations.lease(new JsonBody(source)).get("patched"));
          before = probeEvents(key);
          assertEquals("original:source-stopped", client.request("source-stopped"));
          assertTrue(probeEvents(key) > before); assertEquals(1, capture.drain().size());
          operations.restore(body(key));
        } else {
          operations.restore(body(key));
          assertEquals(false, operations.lease(body(key)).get("patched"));
          assertEquals("updated:probe-stopped", client.request("probe-stopped"));
          assertEquals(1, capture.drain().size());
          operations.instrument(body(key + "-rearmed"));
          operations.restore(new JsonBody(source));
          assertEquals("original:source-stopped", client.request("source-stopped"));
          assertTrue(probeEvents(key + "-rearmed") > 0); assertEquals(1, capture.drain().size());
          operations.restore(body(key + "-rearmed"));
        }
      } finally {
        capture.close();
        if (SourceTransformations.contains(Client.class) || ProbeTransformations.contains(Client.class))
          operations.restore(sourceBody());
      }
      try (ObserverSubscription rearmed = subscribe(key + "-observer-rearmed")) {
        assertEquals("original:rearmed", client.request("rearmed")); assertEquals(1, rearmed.drain().size());
      }
    }
  }

  private static int probeEvents(String key) { return ((List<?>) DiagAgent.drain(key, 0, 100).get("events")).size(); }

  private static void assertProbeRecorded(String key, int minimum) {
    Map<String, Object> batch = DiagAgent.drain(key, 0, 100);
    assertTrue("method tracing disappeared: " + batch,
        ((List<?>) batch.get("events")).size() >= minimum);
  }

  private static JsonBody body(String key) {
    Map<String, Object> body = new LinkedHashMap<>();
    body.put("class", Client.class.getName()); body.put("method", "request(*)");
    body.put("loader", ClassIndex.Entry.of(Client.class).getLoaderHash());
    Map<String, Object> probe = new LinkedHashMap<>();
    probe.put("key", key); probe.put("return", true); body.put("probe", probe);
    body.put("leaseSeconds", -1);
    return new JsonBody(body);
  }

  private static JsonBody sourceBody() {
    Map<String, Object> body = new LinkedHashMap<>();
    body.put("class", Client.class.getName()); body.put("loader", ClassIndex.Entry.of(Client.class).getLoaderHash());
    return new JsonBody(body);
  }

  private static ObserverSubscription subscribe(String id) throws Exception {
    return ObserverRuntime.subscribe("composition", id, Collections.emptyMap());
  }

  private static void registerAdapter() {
    ObserverRuntime.register(new ObserverAdapter() {
      public String id() { return "composition-test"; }
      public Set<String> kinds() { return Collections.singleton("composition"); }
      public boolean matchesType(String name, Set<String> hierarchy) { return name.equals(Client.class.getName()); }
      public boolean matchesMethod(String name, String descriptor) { return name.equals("request"); }
      public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) { return args[0]; }
      public Object onExit(Object token, Object result, Throwable error) {
        ObserverRuntime.emit("composition", Collections.singletonMap("argument", token));
        return result;
      }
    });
  }

  private static final class ForeignTransformer implements ClassFileTransformer {
    public byte[] transform(ClassLoader loader, String name, Class<?> type, ProtectionDomain domain, byte[] bytes) {
      if (type != Client.class) return null;
      ClassReader reader = new ClassReader(bytes);
      ClassWriter writer = new ClassWriter(reader, ClassWriter.COMPUTE_MAXS);
      reader.accept(new ClassVisitor(Opcodes.ASM9, writer) {
        public MethodVisitor visitMethod(int access, String method, String descriptor, String signature, String[] exceptions) {
          MethodVisitor delegate = super.visitMethod(access, method, descriptor, signature, exceptions);
          if (!method.equals("request")) return delegate;
          return new AdviceAdapter(Opcodes.ASM9, delegate, access, method, descriptor) {
            protected void onMethodEnter() {
              String owner = Client.class.getName().replace('.', '/');
              visitFieldInsn(GETSTATIC, owner, "foreignCalls", "I");
              visitInsn(ICONST_1); visitInsn(IADD); visitFieldInsn(PUTSTATIC, owner, "foreignCalls", "I");
            }
          };
        }
      }, ClassReader.EXPAND_FRAMES);
      return writer.toByteArray();
    }
  }
}
