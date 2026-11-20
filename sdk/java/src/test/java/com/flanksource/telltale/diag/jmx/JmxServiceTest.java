package com.flanksource.telltale.diag.jmx;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.lang.management.ManagementFactory;
import java.util.List;
import java.util.Map;
import javax.management.MBeanServer;
import javax.management.ObjectName;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

final class JmxServiceTest {

  private static final String OBJECT_NAME = "example:type=Runtime,name=Primary";
  private final MBeanServer server = ManagementFactory.getPlatformMBeanServer();
  private final JmxService service = new JmxService();

  @Test
  void refusesInvalidBooleanValuesInsteadOfWritingFalse() throws Exception {
    for (String type : new String[] {"boolean", "java.lang.Boolean"}) {
      assertEquals(true, JmxService.convert("true", type));
      assertEquals(false, JmxService.convert("false", type));
      for (Object value : new Object[] {"yes", "", 1}) {
        assertThrows(IllegalArgumentException.class, () -> JmxService.convert(value, type));
      }
    }
  }

  @BeforeEach
  void registerFixture() throws Exception {
    ObjectName name = new ObjectName(OBJECT_NAME);
    if (server.isRegistered(name)) {
      server.unregisterMBean(name);
    }
    server.registerMBean(new TestRuntime(), name);
  }

  @AfterEach
  void unregisterFixture() throws Exception {
    ObjectName name = new ObjectName(OBJECT_NAME);
    if (server.isRegistered(name)) {
      server.unregisterMBean(name);
    }
  }

  @Test
  void returnsDetailedMetadataAndReadableAttributes() throws Exception {
    Map<String, Object> info = service.info(OBJECT_NAME);

    @SuppressWarnings("unchecked")
    List<Map<String, Object>> attributes = (List<Map<String, Object>>) info.get("attributes");
    assertTrue(
        attributes.stream()
            .anyMatch(
                attribute ->
                    "Limit".equals(attribute.get("name"))
                        && "int".equals(attribute.get("type"))
                        && Boolean.TRUE.equals(attribute.get("readable"))
                        && Boolean.TRUE.equals(attribute.get("writable"))));

    @SuppressWarnings("unchecked")
    List<Map<String, Object>> values =
        (List<Map<String, Object>>) service.readAll(OBJECT_NAME).get("attributes");
    assertTrue(
        values.stream()
            .anyMatch(
                attribute ->
                    "Limit".equals(attribute.get("name"))
                        && Integer.valueOf(5).equals(attribute.get("value"))));
  }

  @Test
  void convertsTypedWritesAndInvocationArguments() throws Exception {
    service.write(OBJECT_NAME, "Limit", "7");
    Object value =
        service.invoke(OBJECT_NAME, "multiply", new Object[] {"3"}, new String[] {"int"});

    assertEquals(7, service.read(OBJECT_NAME, "Limit"));
    assertEquals(21, value);
  }

  public interface TestRuntimeMBean {
    int getLimit();

    void setLimit(int limit);

    int multiply(int factor);
  }

  public static final class TestRuntime implements TestRuntimeMBean {
    private int limit = 5;

    @Override
    public int getLimit() {
      return limit;
    }

    @Override
    public void setLimit(int limit) {
      this.limit = limit;
    }

    @Override
    public int multiply(int factor) {
      return limit * factor;
    }
  }
}
