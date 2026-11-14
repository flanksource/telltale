package com.flanksource.telltale;

import static org.junit.Assert.*;

import java.util.*;
import org.junit.Test;

public class DiagnosticsTest {
  @Test
  public void agentCodecPreservesNullJmxResultsAndAttributeValues() throws Exception {
    Diagnostics runtime = new Diagnostics(Collections.singletonMap("allowMutation", true));
    Map<String, Object> params = new LinkedHashMap<>();
    params.put("objectName", "java.lang:type=Memory");
    params.put("operation", "gc");
    Object result = runtime.call("jvm.jmx.invoke", params, true);
    assertNull(result);
    assertEquals("{\"value\":null}", Agent.JSON.toJson(Collections.singletonMap("value", result)));
  }

  @Test
  public void loggerResultSchemaDescribesTheReturnedArray() throws Exception {
    Diagnostics runtime = new Diagnostics(Collections.emptyMap());
    assertTrue(runtime.call("jvm.logging.list", Collections.emptyMap(), false) instanceof List);
    assertEquals("array", DiagnosticSchemas.result("jvm.logging.list").get("type"));
  }

  @Test
  public void readsOneThreadsStackAndRejectsFractionalIds() throws Exception {
    Diagnostics runtime = new Diagnostics(Collections.emptyMap());
    Map<?, ?> result =
        (Map<?, ?>)
            runtime.call(
                "jvm.threads.stack",
                Collections.singletonMap("id", Thread.currentThread().getId()),
                false);
    assertEquals(Thread.currentThread().getId(), ((Map<?, ?>) result.get("thread")).get("id"));
    assertFalse(((List<?>) result.get("frames")).isEmpty());
    assertTrue(
        Agent.JSON
            .toJsonTree(result)
            .getAsJsonObject()
            .getAsJsonObject("thread")
            .get("lockOwnerId")
            .isJsonNull());
    for (Object id : Arrays.asList(1.5, "1", null)) {
      try {
        runtime.call("jvm.threads.stack", Collections.singletonMap("id", id), false);
        fail("invalid thread id accepted: " + id);
      } catch (IllegalArgumentException expected) {
        assertTrue(expected.getMessage(), expected.getMessage().contains("id"));
      }
    }
  }

  @Test
  public void updatesRootLoggingAndRestoresInheritedLevels() throws Exception {
    Diagnostics runtime = new Diagnostics(Collections.singletonMap("allowMutation", true));
    java.util.logging.Logger root = java.util.logging.Logger.getLogger("");
    java.util.logging.Level previous = root.getLevel();
    Map<String, Object> params = new LinkedHashMap<>();
    params.put("name", "");
    params.put("level", "WARNING");
    try {
      runtime.call("jvm.logging.set", params, true);
      assertEquals(java.util.logging.Level.WARNING, root.getLevel());
    } finally {
      root.setLevel(previous);
    }
    java.util.logging.Logger logger = java.util.logging.Logger.getLogger("example.diagnostics");
    java.util.logging.Level inherited = logger.getLevel();
    params.put("name", logger.getName());
    try {
      runtime.call("jvm.logging.set", params, true);
      params.put("level", null);
      runtime.call("jvm.logging.set", params, true);
      assertNull(logger.getLevel());
      params.remove("level");
      try {
        runtime.call("jvm.logging.set", params, true);
        fail("missing level accepted as inheritance");
      } catch (IllegalArgumentException expected) {
        assertTrue(expected.getMessage(), expected.getMessage().contains("level"));
      }
    } finally {
      logger.setLevel(inherited);
    }
  }

  @Test
  public void rejectsScriptArgumentsThatAreNotAnObject() throws Exception {
    Map<String, Object> params = new LinkedHashMap<>();
    params.put("source", "return args");
    params.put("args", Collections.singletonList("wrong"));
    try {
      Scripts.evaluate(
          params,
          getClass().getClassLoader(),
          new com.flanksource.telltale.diag.jmx.JmxService(),
          Collections.emptyMap());
      fail("accepted list arguments");
    } catch (IllegalArgumentException expected) {
      assertTrue(expected.getMessage().contains("args must be an object"));
    }
  }

  @Test
  public void groovyIsIndependentOfTheApplicationsGroovyClasses() throws Exception {
    ClassLoader application =
        new ClassLoader(getClass().getClassLoader()) {
          protected Class<?> loadClass(String name, boolean resolve) throws ClassNotFoundException {
            if (name.startsWith("groovy.") || name.startsWith("org.codehaus.groovy."))
              throw new ClassNotFoundException("application Groovy is incompatible");
            return super.loadClass(name, resolve);
          }
        };
    assertEquals(
        7,
        Scripts.evaluate(
                Collections.singletonMap("source", "return 3 + 4"),
                application,
                new com.flanksource.telltale.diag.jmx.JmxService(),
                Collections.emptyMap())
            .get("value"));
  }

  @Test
  public void patchRequestsFailBeforeLookingUpAClassWhenSourceIsMissing() throws Exception {
    Diagnostics runtime = new Diagnostics(Collections.singletonMap("allowMutation", true));
    try {
      runtime.call(
          "jvm.classes.compile", Collections.singletonMap("class", "fixture.Target"), true);
      fail("missing source accepted");
    } catch (IllegalArgumentException expected) {
      assertTrue(expected.getMessage(), expected.getMessage().contains("source is required"));
    }
  }

  @Test
  public void readsJmxAndRefusesUnknownOperations() throws Exception {
    Diagnostics runtime = new Diagnostics(new HashMap<String, Object>());
    Map<String, Object> params = new HashMap<>();
    params.put("pattern", "java.lang:type=Runtime");
    assertEquals(
        Collections.singletonList("java.lang:type=Runtime"),
        runtime.call("jvm.jmx.search", params, false));
    try {
      runtime.call("unknown", params, true);
      fail("unknown operation accepted");
    } catch (IllegalArgumentException expected) {
      assertTrue(expected.getMessage().contains("unknown"));
    }
  }

  @Test
  public void scriptsRequireMutationAndReturnStructuredOutput() throws Exception {
    Map<String, Object> config = new HashMap<>();
    config.put("allowMutation", true);
    Diagnostics runtime = new Diagnostics(config);
    Map<String, Object> params = new HashMap<>();
    params.put("source", "println 'hello'; return args.value * 2");
    params.put("args", Collections.singletonMap("value", 7));
    try {
      runtime.call("jvm.groovy.eval", params, false);
      fail("mutation accepted");
    } catch (IllegalStateException expected) {
      assertTrue(expected.getMessage().contains("mutation"));
    }
    Map<?, ?> result = (Map<?, ?>) runtime.call("jvm.groovy.eval", params, true);
    assertEquals(14, result.get("value"));
    assertEquals("hello" + System.lineSeparator(), result.get("stdout"));
  }
}
