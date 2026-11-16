package com.flanksource.telltale.observer.sql;

import static org.junit.jupiter.api.Assertions.*;

import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverSubscription;
import java.sql.*;
import java.util.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.jupiter.api.Test;
import reactor.core.publisher.Flux;
import reactor.core.publisher.Mono;

class SqlBatchTest {
  private static Map<String, Object> params() { return Collections.singletonMap("connections", Collections.singletonList("*")); }

  @Test
  void capturesActualJdbcBatchesAndCallableStatementsWithoutParameterValues() throws Exception {
    ObserverRuntime.install(ByteBuddyAgent.install());
    try (Connection connection = DriverManager.getConnection("jdbc:h2:mem:sql-batch");
        Statement ddl = connection.createStatement()) {
      ddl.execute("CREATE TABLE orders(id INT)");
      try (ObserverSubscription capture = ObserverRuntime.subscribe("sql", "jdbc-batch", params());
          PreparedStatement statement = connection.prepareStatement("INSERT INTO orders VALUES (?)")) {
        statement.setInt(1, 11);
        statement.addBatch();
        statement.setInt(1, 12);
        statement.addBatch();
        assertArrayEquals(new int[] {1, 1}, statement.executeBatch());
        statement.setInt(1, 13);
        assertEquals(1, statement.executeUpdate());
        statement.setInt(1, 14);
        assertEquals(1L, statement.executeLargeUpdate());
        statement.setInt(1, 15);
        statement.addBatch();
        statement.setInt(1, 16);
        statement.addBatch();
        assertArrayEquals(new long[] {1, 1}, statement.executeLargeBatch());
        try (CallableStatement call = connection.prepareCall("CALL ABS(?)")) {
          call.setInt(1, -23);
          assertTrue(call.execute());
        }
        List<Map<String, Object>> rows = capture.drain();
        assertEquals(5, rows.size());
        assertEquals(2L, rows.get(0).get("batchSize"));
        assertFalse(rows.get(1).containsKey("batchSize"));
        assertTrue(rows.get(4).get("sql").toString().contains("ABS"));
        assertEquals(Arrays.asList(2L, 1L, 1L, 2L, -1L), Arrays.asList(rows.get(0).get("rows"), rows.get(1).get("rows"), rows.get(2).get("rows"), rows.get(3).get("rows"), rows.get(4).get("rows")));
        for (Map<String, Object> row : rows) assertFalse(row.containsKey("args"));
      }
    }
  }

  @Test
  void countsSavedAndCurrentR2dbcBindingsAndNativeBatchEntries() throws Exception {
    ObserverRuntime.install(ByteBuddyAgent.install());
    io.r2dbc.spi.Connection connection = Mono.from(io.r2dbc.spi.ConnectionFactories.get("r2dbc:h2:mem:///sql-r2dbc-batch").create()).block();
    try {
      Flux.from(connection.createStatement("CREATE TABLE orders(id INT)").execute()).flatMap(io.r2dbc.spi.Result::getRowsUpdated).blockLast();
      try (ObserverSubscription capture = ObserverRuntime.subscribe("sql", "r2dbc-batch", params())) {
        io.r2dbc.spi.Statement statement = connection.createStatement("INSERT INTO orders VALUES ($1)");
        statement.bind(0, 11).add().bind(0, 12);
        assertEquals(Long.valueOf(1), Flux.from(statement.execute()).flatMap(io.r2dbc.spi.Result::getRowsUpdated).blockLast());
        io.r2dbc.spi.Batch batch = connection.createBatch().add("INSERT INTO orders VALUES (13)").add("INSERT INTO orders VALUES (14)");
        assertEquals(Long.valueOf(1), Flux.from(batch.execute()).flatMap(io.r2dbc.spi.Result::getRowsUpdated).blockLast());
        List<Map<String, Object>> rows = capture.drain();
        assertEquals(2, rows.size());
        assertEquals(2L, rows.get(0).get("batchSize"));
        assertEquals(2L, rows.get(1).get("batchSize"));
        assertFalse(rows.get(1).get("sql").toString().contains("13"));
      }
    } finally {
      Mono.from(connection.close()).block();
    }
  }
}
