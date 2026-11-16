package com.flanksource.telltale.observer.sql;

import static org.junit.Assert.*;

import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverSubscription;
import io.r2dbc.spi.*;
import java.time.Duration;
import java.util.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.Test;
import org.springframework.data.annotation.Id;
import org.springframework.data.r2dbc.core.R2dbcEntityTemplate;
import org.springframework.data.relational.core.mapping.Table;
import org.springframework.r2dbc.core.DatabaseClient;
import reactor.core.publisher.Flux;
import reactor.core.publisher.Mono;

public class SpringR2dbcObserverTest {
  @Test public void observesExistingDatabaseClientAndEntityTemplateThroughResultConsumption() throws Exception {
    ConnectionFactory factory = ConnectionFactories.get("r2dbc:h2:mem:///spring-reactive-" + UUID.randomUUID());
    Connection connection = Mono.from(factory.create()).block(Duration.ofSeconds(5));
    assertNotNull(connection);
    try {
      Flux.from(connection.createStatement("CREATE TABLE spring_reactive_orders(id BIGINT PRIMARY KEY,payload VARCHAR(100))").execute())
          .flatMap(result -> result.getRowsUpdated()).blockLast(Duration.ofSeconds(5));
      Flux.from(connection.createStatement("INSERT INTO spring_reactive_orders(id,payload) VALUES(7,'private-reactive-value')").execute())
          .flatMap(result -> result.getRowsUpdated()).blockLast(Duration.ofSeconds(5));
      DatabaseClient client = DatabaseClient.create(factory);
      R2dbcEntityTemplate template = new R2dbcEntityTemplate(factory);
      ObserverRuntime.install(ByteBuddyAgent.install());
      try (ObserverSubscription capture = ObserverRuntime.subscribe("sql", "spring-r2dbc", Collections.singletonMap("connections", Collections.singletonList("*")));
          ObserverSubscription top = ObserverRuntime.subscribe("sql_top", "spring-r2dbc-top", Collections.emptyMap())) {
        Mono<String> query = client.sql("SELECT payload FROM spring_reactive_orders WHERE id=:id").bind("id", 7L)
            .map((row, metadata) -> row.get("payload", String.class)).one();
        assertTrue("building a cold query must not create active SQL", top.snapshot().isEmpty());
        assertTrue(capture.drain().isEmpty());
        assertEquals("private-reactive-value", query.block(Duration.ofSeconds(5)));
        List<Order> orders = template.select(Order.class).all().collectList().block(Duration.ofSeconds(5));
        assertNotNull(orders);
        assertEquals(1, orders.size());
        assertEquals(Long.valueOf(7), orders.get(0).id);
        assertEquals("private-reactive-value", orders.get(0).payload);
        List<Map<String, Object>> rows = completed(capture);
        assertEquals("each consumed framework query must be observed once", 2, rows.size());
        for (Map<String, Object> row : rows) {
          assertEquals("r2dbc", row.get("api"));
          assertNotNull(row.get("operationId"));
          assertTrue(((Number) row.get("durationNs")).longValue() >= 0);
          assertFalse(row.containsKey("sqlUnavailableReason"));
          assertFalse(row.toString().contains("private-reactive-value"));
          assertFalse(row.containsKey("error"));
        }
        assertNotEquals(rows.get(0).get("operationId"), rows.get(1).get("operationId"));
        assertTrue(top.snapshot().isEmpty());
      }
    } finally { Mono.from(connection.close()).block(Duration.ofSeconds(5)); }
  }

  private static List<Map<String, Object>> completed(ObserverSubscription capture) throws Exception {
    List<Map<String, Object>> rows = new ArrayList<>();
    long deadline = System.nanoTime() + java.util.concurrent.TimeUnit.SECONDS.toNanos(5);
    while (rows.size() < 2) {
      rows.addAll(capture.drain());
      if (System.nanoTime() >= deadline) fail("the two consumed Spring R2DBC queries did not complete");
      Thread.sleep(10);
    }
    return rows;
  }

  @Table("spring_reactive_orders")
  public static class Order {
    @Id public Long id;
    public String payload;
  }
}
