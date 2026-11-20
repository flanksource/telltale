package com.flanksource.telltale.diag.jvm;

import static org.junit.Assert.*;

import com.flanksource.telltale.diag.hotpatch.ClassIndex;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.lang.instrument.Instrumentation;
import java.lang.reflect.Proxy;
import java.util.*;
import org.junit.Test;

public class NativeTelemetryServiceTest {
  @Test public void readsEveryApplicationLoaderWithoutArmingOrJmx() throws Exception {
    Class<?> first = isolatedTelemetry();
    Class<?> second = isolatedTelemetry();
    ClassLoader caller = Thread.currentThread().getContextClassLoader();
    Map<String, Object> result = service(String.class, second, first).snapshot();
    assertNotNull(result.get("capturedAt"));
    List<?> applications = (List<?>) result.get("applications");
    assertEquals(2, applications.size());
    Set<String> loaders = new HashSet<>();
    for (Object application : applications) {
      Map<?, ?> row = (Map<?, ?>) application;
      loaders.add((String) row.get("loader"));
      assertEquals(true, ((Map<?, ?>) row.get("status")).get("armed"));
      Map<?, ?> histogram = (Map<?, ?>) ((List<?>) row.get("histograms")).get(0);
      assertEquals(9007199254740993L, histogram.get("count"));
    }
    assertEquals(new HashSet<>(Arrays.asList(ClassIndex.hash(first.getClassLoader()), ClassIndex.hash(second.getClassLoader()))), loaders);
    assertSame(first.getClassLoader(), first.getField("context").get(null));
    assertSame(second.getClassLoader(), second.getField("context").get(null));
    assertSame(caller, Thread.currentThread().getContextClassLoader());
  }

  @Test public void reportsNoTelemetryWithoutLoadingAnApplicationClass() throws Exception {
    assertEquals(Collections.emptyList(), service(String.class).snapshot().get("applications"));
  }

  @Test public void preservesProviderFailuresAndRestoresTheCallerLoader() throws Exception {
    Class<?> telemetry = isolatedTelemetry();
    telemetry.getField("fail").setBoolean(null, true);
    ClassLoader caller = Thread.currentThread().getContextClassLoader();
    try {
      Exception error = assertThrows(Exception.class, () -> service(telemetry).snapshot());
      assertTrue(error.toString(), error.toString().contains("telemetry fixture failure"));
      assertSame(caller, Thread.currentThread().getContextClassLoader());
    } finally {
      telemetry.getField("fail").setBoolean(null, false);
    }
  }

  @Test public void olderTelemetryIsAnExplicitErrorAndTheOperationIsReadOnly() throws Exception {
    org.objectweb.asm.ClassWriter writer = new org.objectweb.asm.ClassWriter(0);
    writer.visit(org.objectweb.asm.Opcodes.V1_8, org.objectweb.asm.Opcodes.ACC_PUBLIC,
        "com/oipa/telemetry/Telemetry", null, "java/lang/Object", null);
    writer.visitEnd();
    Class<?> older = new ClassLoader(getClass().getClassLoader()) {
      Class<?> define() {
        byte[] bytes = writer.toByteArray();
        return defineClass("com.oipa.telemetry.Telemetry", bytes, 0, bytes.length);
      }
    }.define();
    IllegalStateException error = assertThrows(IllegalStateException.class, () -> service(older).snapshot());
    assertTrue(error.getMessage(), error.getMessage().contains("update the telemetry extension and restart"));
    assertEquals(com.flanksource.telltale.Scope.READ,
        new com.flanksource.telltale.Diagnostics(Collections.emptyMap()).scope("jvm.telemetry"));
  }

  private NativeTelemetryService service(Class<?>... types) {
    Instrumentation instrumentation = (Instrumentation) Proxy.newProxyInstance(getClass().getClassLoader(), new Class<?>[]{Instrumentation.class}, (proxy, method, args) -> {
      if (method.getName().equals("getAllLoadedClasses")) return types;
      throw new AssertionError("unexpected instrumentation call " + method.getName());
    });
    return new NativeTelemetryService(instrumentation);
  }

  private Class<?> isolatedTelemetry() throws Exception {
    String name = "com.oipa.telemetry.Telemetry";
    ByteArrayOutputStream bytes = new ByteArrayOutputStream();
    try (InputStream input = getClass().getClassLoader().getResourceAsStream(name.replace('.', '/') + ".class")) {
      assertNotNull(input);
      byte[] buffer = new byte[4096];
      for (int length; (length = input.read(buffer)) != -1;) bytes.write(buffer, 0, length);
    }
    return new ClassLoader(getClass().getClassLoader()) {
      @Override protected Class<?> loadClass(String requested, boolean resolve) throws ClassNotFoundException {
        if (!requested.equals(name)) return super.loadClass(requested, resolve);
        Class<?> type = findLoadedClass(requested);
        if (type == null) type = defineClass(requested, bytes.toByteArray(), 0, bytes.size());
        if (resolve) resolveClass(type);
        return type;
      }
    }.loadClass(name);
  }
}
