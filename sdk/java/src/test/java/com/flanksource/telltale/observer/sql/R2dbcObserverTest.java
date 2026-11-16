package com.flanksource.telltale.observer.sql;

import static org.junit.jupiter.api.Assertions.*;

import io.r2dbc.spi.*;
import java.util.*;
import java.util.concurrent.atomic.AtomicBoolean;
import org.junit.jupiter.api.Test;
import org.reactivestreams.Publisher;
import reactor.core.publisher.Flux;
import reactor.core.publisher.Mono;

class R2dbcObserverTest {
  @Test
  @SuppressWarnings("unchecked")
  void observesExistingFactoryAndStatementAtSubscriptionThroughResultConsumption() {
    Connection connection = Mono.from(ConnectionFactories.get("r2dbc:h2:mem:///sql-observer-reactive").create()).block();
    try {
      Statement statement = connection.createStatement("SELECT 1");
      List<Map<String, Object>> rows = new ArrayList<>();
      SqlObserver observer = new SqlObserver(new SqlObserver.Options(rows::add, () -> true));
      Object token = observer.onEnter("test", "execute", "", statement, new Object[0]);
      Publisher<? extends Result> publisher = (Publisher<? extends Result>) observer.onExit(token, statement.execute(), null);
      assertTrue(rows.isEmpty());
      assertTrue(observer.snapshot("sql_top").isEmpty());
      List<? extends Result> results = Flux.from(publisher).collectList().block();
      assertEquals(1, observer.snapshot("sql_top").size());
      assertEquals("awaiting-consumption", ((Map<?, ?>) observer.snapshot("sql_top").get(0).get("attributes")).get("phase"));
      assertTrue(rows.isEmpty());
      assertEquals(Integer.valueOf(1), Mono.from(results.get(0).map((row, metadata) -> {
        assertEquals("consuming", ((Map<?, ?>) observer.snapshot("sql_top").get(0).get("attributes")).get("phase"));
        return row.get(0, Integer.class);
      })).block());
      assertEquals(1, rows.size());
      assertTrue(observer.snapshot("sql_top").isEmpty());
      assertEquals("r2dbc", rows.get(0).get("api"));
      assertEquals("statement-before-instrumentation", rows.get(0).get("sqlUnavailableReason"));
      assertEquals(-1L, rows.get(0).get("rows"));
    } finally {
      Mono.from(connection.close()).block();
    }
  }

  @Test
  @SuppressWarnings("unchecked")
  void keepsNewStatementSqlSanitizedAndSeparateSubscriptionsDistinct() {
    Connection connection = Mono.from(ConnectionFactories.get("r2dbc:h2:mem:///sql-observer-repeated").create()).block();
    try {
      List<Map<String, Object>> rows = new ArrayList<>();
      SqlObserver observer = new SqlObserver(new SqlObserver.Options(rows::add, () -> true));
      String sql = "SELECT 'confidential-value'";
      Object creation = observer.onEnter("test", "createStatement", "", connection, new Object[] {sql});
      Statement statement = connection.createStatement(sql);
      observer.onExit(creation, statement, null);
      Object token = observer.onEnter("test", "execute", "", statement, new Object[0]);
      Publisher<? extends Result> publisher = (Publisher<? extends Result>) observer.onExit(token, statement.execute(), null);
      for (int subscription = 0; subscription < 2; subscription++)
        assertEquals("confidential-value", Flux.from(publisher).flatMap(result -> result.map((row, metadata) -> row.get(0, String.class))).blockLast());
      assertEquals(2, rows.size());
      assertNotEquals(rows.get(0).get("operationId"), rows.get(1).get("operationId"));
      assertFalse(rows.get(0).get("sql").toString().contains("confidential-value"));
    } finally {
      Mono.from(connection.close()).block();
    }
  }

  @Test
  @SuppressWarnings("unchecked")
  void doesNotStartADeferredExecutionAfterItsCaptureEnds() {
    Connection connection = Mono.from(ConnectionFactories.get("r2dbc:h2:mem:///sql-observer-late").create()).block();
    try {
      List<Map<String, Object>> rows = new ArrayList<>();
      AtomicBoolean enabled = new AtomicBoolean(true);
      SqlObserver observer = new SqlObserver(new SqlObserver.Options(rows::add, enabled::get));
      Statement statement = connection.createStatement("SELECT 1");
      Object token = observer.onEnter("test", "execute", "", statement, new Object[0]);
      Publisher<? extends Result> publisher = (Publisher<? extends Result>) observer.onExit(token, statement.execute(), null);
      enabled.set(false);
      observer.onDisarm("sql");
      List<? extends Result> results = Flux.from(publisher).collectList().block();
      assertTrue(observer.snapshot("sql_top").isEmpty());
      assertEquals(Integer.valueOf(1), Mono.from(results.get(0).map((row, metadata) -> row.get(0, Integer.class))).block());
      assertTrue(rows.isEmpty());
    } finally {
      Mono.from(connection.close()).block();
    }
  }
}
