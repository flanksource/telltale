package com.flanksource.telltale.observer.sql;

import static org.junit.jupiter.api.Assertions.*;

import java.net.URL;
import java.util.Map;
import org.junit.jupiter.api.Test;

class SqlHelperBytecodeTest {
  @Test
  void eagerlyLoadsThePinnedBridgeAndProxyImplementation() {
    Map<String, byte[]> classes = SqlHelperBytecode.load(getClass().getClassLoader());
    assertTrue(classes.containsKey("com.flanksource.telltale.observer.sql.R2dbcBridge"));
    assertTrue(classes.containsKey("com.flanksource.telltale.observer.sql.R2dbcBridge$QueryListener"));
    assertTrue(classes.containsKey("io.r2dbc.proxy.callback.ProxyConfig"));
    assertTrue(classes.containsKey("io.r2dbc.proxy.callback.ResultInvocationSubscriber"));
    assertFalse(classes.keySet().stream().anyMatch(name -> name.startsWith("io.r2dbc.spi.") || name.startsWith("reactor.")));
    assertThrows(UnsupportedOperationException.class, () -> classes.put("unexpected", new byte[0]));
  }

  @Test
  void failsAtInitializationWhenThePackagedHelperIsMissing() {
    ClassLoader missing = new ClassLoader() { public URL getResource(String name) { return null; } };
    IllegalStateException error = assertThrows(IllegalStateException.class, () -> SqlHelperBytecode.load(missing));
    assertTrue(error.getMessage().contains("SQL helper bytecode is missing"));
  }
}
