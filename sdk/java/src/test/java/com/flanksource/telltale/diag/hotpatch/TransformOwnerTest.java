package com.flanksource.telltale.diag.hotpatch;

import static org.junit.Assert.*;

import com.flanksource.telltale.diag.agent.DiagAgent;
import com.flanksource.telltale.observer.*;
import java.io.*;
import java.lang.instrument.*;
import java.util.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.Test;

public class TransformOwnerTest {
  public static final class Client {
    public String request(String value) { return "original:" + value; }
    public String untouched() { return "untouched"; }
  }

  @Test
  public void restoresSourceEditsFromANewDiagnosticsClassLoader() throws Exception {
    Instrumentation instrumentation = ByteBuddyAgent.install();
    DiagAgent.INSTRUMENTATION = instrumentation;
    ObserverRuntime.install(instrumentation);
    ObserverRuntime.register(new SourceObserver());
    try (ObserverSubscription capture = ObserverRuntime.subscribe("redeploy-source", "redeploy-source", Collections.emptyMap())) {
    DiagnosticsLoader previousLoader = new DiagnosticsLoader();
    Class<?> localRuntime = previousLoader.loadClass(ObserverRuntime.class.getName());
    assertNotSame(ObserverRuntime.class, localRuntime);
    assertEquals(true, localRuntime.getMethod("installed").invoke(null));
    Class<?> sourceService = previousLoader.loadClass(PatchService.class.getName());
    java.lang.reflect.Method sourceRead = sourceService.getDeclaredMethod("sourceBytes", Instrumentation.class, Class.class);
    sourceRead.setAccessible(true);
    byte[] source = (byte[]) sourceRead.invoke(null, instrumentation, Client.class);
    MethodPatchService compiler = new MethodPatchService();
    MethodPatchService.Result compiled = compiler.compile(Client.class.getName(), Client.class,
        source, MethodSelector.parse("request(String)"),
        "public String request(String value) { return \"patched:\" + value; }", null);
    assertTrue(compiled.isSuccess());
    byte[] replacement = compiler.splice(Client.class.getName(), compiled, Client.class.getClassLoader());
    Client client = new Client();
    Class<?> previous = previousLoader.loadClass(SourceTransformations.class.getName());
    Class<?> redeployed = new DiagnosticsLoader().loadClass(SourceTransformations.class.getName());
    assertNotSame(previous, redeployed);
    try {
      dispatch(previous, "apply", new Object[] {instrumentation, Collections.singletonList(new ClassDefinition(Client.class, replacement))});
      assertEquals("patched:before-redeploy", client.request("before-redeploy"));
      assertEquals("untouched", client.untouched()); assertEquals(1, capture.drain().size());
      dispatch(redeployed, "restore", new Object[] {Collections.singletonList(Client.class)});
      assertEquals("original:after-redeploy", client.request("after-redeploy"));
      assertEquals("untouched", client.untouched()); assertEquals(1, capture.drain().size());
      instrumentation.retransformClasses(Client.class);
      assertEquals("original:after-refresh", client.request("after-refresh"));
    } finally { if (SourceTransformations.contains(Client.class)) SourceTransformations.restore(Collections.singletonList(Client.class)); }
    }
  }

  @Test
  public void restoresMethodProbesFromANewDiagnosticsClassLoader() throws Exception {
    Instrumentation instrumentation = ByteBuddyAgent.install();
    DiagAgent.INSTRUMENTATION = instrumentation;
    String key = "redeploy-method-probe";
    ClassIndex.Entry entry = ClassIndex.Entry.of(Client.class);
    Map<String, Object> options = new LinkedHashMap<>();
    options.put("method", "request(String)"); options.put("return", true);
    AgentBridge.openProbe(key, entry.getLoaderHash(), entry.getName(), options);
    Class<?> previous = new DiagnosticsLoader().loadClass(ProbeTransformations.class.getName());
    Class<?> redeployed = new DiagnosticsLoader().loadClass(ProbeTransformations.class.getName());
    try {
      Object result = dispatch(previous, "arm", new Object[] {instrumentation, Client.class, key});
      assertTrue(result instanceof Map);
      new Client().request("before-redeploy");
      int before = ((List<?>) DiagAgent.drain(key, 0, 100).get("events")).size();
      assertTrue(before > 0);
      dispatch(redeployed, "restore", new Object[] {Client.class});
      new Client().request("after-redeploy");
      assertEquals(before, ((List<?>) DiagAgent.drain(key, 0, 100).get("events")).size());
    } finally {
      if (ProbeTransformations.contains(Client.class)) ProbeTransformations.restore(Client.class);
      AgentBridge.releaseProbe(key, entry.getLoaderHash(), entry.getName());
    }
  }

  private static Object dispatch(Class<?> helper, String operation, Object[] args) throws Exception {
    return helper.getMethod("dispatch", String.class, Object[].class).invoke(null, operation, args);
  }

  private static final class SourceObserver implements ObserverAdapter {
    public String id() { return "redeploy-source-test"; }
    public Set<String> kinds() { return Collections.singleton("redeploy-source"); }
    public boolean matchesType(String name, Set<String> hierarchy) { return name.equals(Client.class.getName()); }
    public boolean matchesMethod(String name, String descriptor) { return name.equals("untouched"); }
    public Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args) { return Boolean.TRUE; }
    public Object onExit(Object token, Object result, Throwable error) {
      ObserverRuntime.emit("redeploy-source", Collections.singletonMap("value", result)); return result;
    }
  }

  private static final class DiagnosticsLoader extends ClassLoader {
    DiagnosticsLoader() { super(TransformOwnerTest.class.getClassLoader()); }
    protected Class<?> loadClass(String name, boolean resolve) throws ClassNotFoundException {
      if (!name.startsWith("com.flanksource.telltale.diag.hotpatch.")
          && !name.startsWith("com.flanksource.telltale.observer.")) return super.loadClass(name, resolve);
      Class<?> type = findLoadedClass(name);
      if (type == null) {
        try (InputStream input = getParent().getResourceAsStream(name.replace('.', '/') + ".class")) {
          if (input == null) throw new ClassNotFoundException(name);
          ByteArrayOutputStream output = new ByteArrayOutputStream();
          byte[] buffer = new byte[8192];
          for (int count; (count = input.read(buffer)) != -1;) output.write(buffer, 0, count);
          byte[] bytes = output.toByteArray();
          type = defineClass(name, bytes, 0, bytes.length);
        } catch (IOException error) { throw new ClassNotFoundException(name, error); }
      }
      if (resolve) resolveClass(type);
      return type;
    }
  }
}
