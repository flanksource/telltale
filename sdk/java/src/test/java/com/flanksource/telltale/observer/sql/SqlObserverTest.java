package com.flanksource.telltale.observer.sql;

import static org.junit.jupiter.api.Assertions.*;

import java.sql.*;
import java.util.*;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;

class SqlObserverTest {
  @Test
  void observesSqlWithoutCallingDriverMetadataMethods() throws Exception {
    AtomicInteger metadataCalls = new AtomicInteger();
    List<Map<String, Object>> rows = new ArrayList<>();
    SqlObserver observer = new SqlObserver(new SqlObserver.Options(rows::add, () -> true));
    try (Connection connection = DriverManager.getConnection("jdbc:h2:mem:sql-passive")) {
      Connection guarded = (Connection) java.lang.reflect.Proxy.newProxyInstance(getClass().getClassLoader(), new Class<?>[] {Connection.class}, (proxy, method, args) -> {
        if (method.getName().equals("getMetaData") || method.getName().equals("getCatalog")) {
          metadataCalls.incrementAndGet();
          throw new AssertionError("observer invoked driver metadata API");
        }
        try { return method.invoke(connection, args); }
        catch (java.lang.reflect.InvocationTargetException error) { throw error.getCause(); }
      });
      Object prepare = observer.onEnter("test", "prepareStatement", "", guarded, new Object[] {"SELECT 1"});
      try (PreparedStatement statement = guarded.prepareStatement("SELECT 1")) {
        observer.onExit(prepare, statement, null);
        Object execute = observer.onEnter("test", "execute", "", statement, new Object[0]);
        observer.onExit(execute, statement.execute(), null);
      }
    }
    assertEquals(0, metadataCalls.get());
    assertEquals(1, rows.size());
    assertEquals("org.h2.jdbc.JdbcPreparedStatement", rows.get(0).get("driver"));
    assertFalse(rows.get(0).containsKey("database"));
    assertFalse(rows.get(0).containsKey("engine"));
  }

  @Test
  void capturesPreparedExecutionAndLiveSnapshotWithoutLeakingValues() throws Exception {
    List<Map<String, Object>> rows = new ArrayList<>();
    SqlObserver observer = new SqlObserver(new SqlObserver.Options(rows::add, () -> true));
    try (Connection connection = DriverManager.getConnection("jdbc:h2:mem:sql-observer")) {
      String query = "SELECT 'private-value' WHERE ? = 1";
      Object prepare = observer.onEnter("test", "prepareStatement", "", connection, new Object[] {query});
      try (PreparedStatement statement = connection.prepareStatement(query)) {
        observer.onExit(prepare, statement, null);
        statement.setInt(1, 1);
        Object execution = observer.onEnter("test", "executeQuery", "", statement, new Object[0]);
        List<Map<String, Object>> active = observer.snapshot("sql_top");
        assertEquals(1, active.size());
        try (ResultSet result = statement.executeQuery()) {
          assertTrue(result.next());
          observer.onExit(execution, result, null);
        }
        assertTrue(observer.snapshot("sql_top").isEmpty());
      }
    }
    assertEquals(1, rows.size());
    assertEquals("jdbc", rows.get(0).get("api"));
    assertEquals(-1L, rows.get(0).get("rows"));
    assertFalse(rows.get(0).get("sql").toString().contains("private-value"));
    assertTrue(((Number) rows.get(0).get("durationNs")).longValue() >= 0);
  }

  @Test
  void recordsPreexistingPreparedStatementsWithExplicitMissingSql() throws Exception {
    List<Map<String, Object>> rows = new ArrayList<>();
    SqlObserver observer = new SqlObserver(new SqlObserver.Options(rows::add, () -> true));
    try (Connection connection = DriverManager.getConnection("jdbc:h2:mem:sql-old");
        PreparedStatement statement = connection.prepareStatement("SELECT 1")) {
      Object execution = observer.onEnter("test", "execute", "", statement, new Object[0]);
      observer.onExit(execution, statement.execute(), null);
    }
    assertEquals(1, rows.size());
    assertEquals("prepared-before-instrumentation", rows.get(0).get("sqlUnavailableReason"));
    assertFalse(rows.get(0).containsKey("sql"));
  }

  @Test
  void suppressesNestedJdbcDelegateExecutionsAndKeepsFailures() throws Exception {
    List<Map<String, Object>> rows = new ArrayList<>();
    SqlObserver observer = new SqlObserver(new SqlObserver.Options(rows::add, () -> true));
    try (Connection connection = DriverManager.getConnection("jdbc:h2:mem:sql-errors");
        Statement statement = connection.createStatement()) {
      Object outer = observer.onEnter("test", "execute", "", statement, new Object[] {"SELECT * FROM missing_table"});
      Object nested = observer.onEnter("test", "executeQuery", "", statement, new Object[] {"SELECT * FROM missing_table"});
      SQLException failure = assertThrows(SQLException.class, () -> statement.execute("SELECT * FROM missing_table"));
      observer.onExit(nested, null, failure);
      observer.onExit(outer, null, failure);
    }
    assertEquals(1, rows.size());
    assertTrue(rows.get(0).get("error").toString().contains("SQLSyntaxErrorException"));
    assertTrue(observer.snapshot("sql_top").isEmpty());
  }

  @Test
  void enrichesAnExistingPreparedStatementFromTheLiveOtelTapWithoutDuplicatingIt() throws Exception {
    List<Map<String, Object>> rows = new ArrayList<>();
    SqlObserver observer = new SqlObserver(new SqlObserver.Options(rows::add, () -> true));
    try (Connection connection = DriverManager.getConnection("jdbc:h2:mem:sql-tap");
        PreparedStatement statement = connection.prepareStatement("SELECT 'confidential-value'")) {
      Object execution = observer.onEnter("test", "execute", "", statement, new Object[0]);
      Map<String, Object> operation = new HashMap<>();
      operation.put("signal", "sql");
      operation.put("traceId", "00112233445566778899aabbccddeeff");
      operation.put("spanId", "0011223344556677");
      operation.put("attributes", Collections.singletonMap("db.query.text", "SELECT 'confidential-value'"));
      observer.onOperation(operation);
      observer.onExit(execution, statement.execute(), null);
    }
    assertEquals(1, rows.size());
    assertFalse(rows.get(0).containsKey("sqlUnavailableReason"));
    assertFalse(rows.get(0).get("sql").toString().contains("confidential-value"));
    assertEquals("0011223344556677", rows.get(0).get("spanId"));
  }
}
