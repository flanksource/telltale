package com.flanksource.telltale.observer.sql;

import static org.junit.jupiter.api.Assertions.*;

import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverSubscription;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.util.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.jupiter.api.Test;
import reactor.core.publisher.Flux;
import reactor.core.publisher.Mono;

class SqlAttachTest {
  @Test
  void retransformsAnExistingJdbcStatementWithoutRestartingOrReplacingIt() throws Exception {
    try (java.sql.Connection connection = DriverManager.getConnection("jdbc:h2:mem:sql-attach");
        PreparedStatement statement = connection.prepareStatement("SELECT 1")) {
      assertTrue(statement.execute());
      ObserverRuntime.install(ByteBuddyAgent.install());
      try (ObserverSubscription capture = ObserverRuntime.subscribe("sql", "jdbc-existing", Collections.singletonMap("connections", Collections.singletonList("*")))) {
        assertTrue(statement.execute());
        List<Map<String, Object>> rows = capture.drain();
        assertEquals(1, rows.size());
        assertEquals("prepared-before-instrumentation", rows.get(0).get("sqlUnavailableReason"));
        assertEquals(-1L, rows.get(0).get("rows"));
      }
      assertTrue(statement.execute());
    }
  }

  @Test
  void attachesToAnExistingR2dbcFactoryConnectionAndStatementThroughConsumption() throws Exception {
    io.r2dbc.spi.ConnectionFactory factory = io.r2dbc.spi.ConnectionFactories.get("r2dbc:h2:mem:///sql-attach-reactive");
    io.r2dbc.spi.Connection connection = Mono.from(factory.create()).block();
    try {
      io.r2dbc.spi.Statement statement = connection.createStatement("SELECT 1");
      assertEquals(Integer.valueOf(1), Flux.from(statement.execute()).flatMap(result -> result.map((row, metadata) -> row.get(0, Integer.class))).blockLast());
      ObserverRuntime.install(ByteBuddyAgent.install());
      try (ObserverSubscription capture = ObserverRuntime.subscribe("sql", "r2dbc-existing", Collections.singletonMap("connections", Collections.singletonList("*")));
          ObserverSubscription top = ObserverRuntime.subscribe("sql_top", "r2dbc-existing-top", Collections.emptyMap())) {
        List<? extends io.r2dbc.spi.Result> results = Flux.from(statement.execute()).collectList().block();
        assertEquals(1, top.snapshot().size());
        assertTrue(capture.drain().isEmpty());
        assertEquals(Integer.valueOf(1), Mono.from(results.get(0).map((row, metadata) -> row.get(0, Integer.class))).block());
        List<Map<String, Object>> rows = capture.drain();
        assertEquals(1, rows.size());
        assertEquals("statement-before-instrumentation", rows.get(0).get("sqlUnavailableReason"));
        assertTrue(top.snapshot().isEmpty());
      }
    } finally {
      Mono.from(connection.close()).block();
    }
  }

  @Test
  void closingOneCaptureMarksItsUnconsumedResultIncompleteWithoutEndingAnotherCapture() throws Exception {
    ObserverRuntime.install(ByteBuddyAgent.install());
    io.r2dbc.spi.Connection connection = Mono.from(io.r2dbc.spi.ConnectionFactories.get("r2dbc:h2:mem:///sql-incomplete").create()).block();
    Map<String, Object> params = Collections.singletonMap("connections", Collections.singletonList("*"));
    try (ObserverSubscription first = ObserverRuntime.subscribe("sql", "incomplete-first", params);
        ObserverSubscription second = ObserverRuntime.subscribe("sql", "incomplete-second", params);
        ObserverSubscription top = ObserverRuntime.subscribe("sql_top", "incomplete-top", Collections.emptyMap())) {
      List<? extends io.r2dbc.spi.Result> results = Flux.from(connection.createStatement("SELECT 1").execute()).collectList().block();
      first.close();
      List<Map<String, Object>> incomplete = first.drain();
      assertEquals(1, incomplete.size());
      assertEquals(true, incomplete.get(0).get("incomplete"));
      assertEquals("capture-ended", incomplete.get(0).get("completion"));
      assertEquals("awaiting-consumption", incomplete.get(0).get("phase"));
      assertTrue(second.drain().isEmpty());
      assertEquals(1, top.snapshot().size());
      assertEquals(Integer.valueOf(1), Mono.from(results.get(0).map((row, metadata) -> row.get(0, Integer.class))).block());
      List<Map<String, Object>> completed = second.drain();
      assertEquals(1, completed.size());
      assertFalse(completed.get(0).containsKey("incomplete"));
      assertEquals(incomplete.get(0).get("operationId"), completed.get(0).get("operationId"));
      assertTrue(top.snapshot().isEmpty());
    } finally {
      Mono.from(connection.close()).block();
    }
  }
}
