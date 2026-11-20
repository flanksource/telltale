package com.flanksource.telltale.observer.sql;

import static org.junit.Assert.*;

import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverSubscription;
import com.zaxxer.hikari.HikariConfig;
import com.zaxxer.hikari.HikariDataSource;
import java.util.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.Test;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.core.namedparam.NamedParameterJdbcTemplate;
import org.springframework.jdbc.datasource.DataSourceTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

public class SpringJdbcObserverTest {
  @Test public void observesExistingPooledTemplatesAndPreservesTransactionRollback() throws Exception {
    HikariConfig config = new HikariConfig();
    config.setJdbcUrl("jdbc:h2:mem:spring-jdbc-" + UUID.randomUUID());
    config.setMaximumPoolSize(1);
    config.setMinimumIdle(1);
    try (HikariDataSource pool = new HikariDataSource(config)) {
      JdbcTemplate jdbc = new JdbcTemplate(pool);
      NamedParameterJdbcTemplate named = new NamedParameterJdbcTemplate(jdbc);
      TransactionTemplate transaction = new TransactionTemplate(new DataSourceTransactionManager(pool));
      jdbc.execute("CREATE TABLE spring_orders(id BIGINT PRIMARY KEY, payload VARCHAR(100))");
      jdbc.update("INSERT INTO spring_orders(id,payload) VALUES (?,?)", 7L, "private-initial-value");
      ObserverRuntime.install(ByteBuddyAgent.install());
      try (ObserverSubscription capture = ObserverRuntime.subscribe("sql", "spring-jdbc", Collections.singletonMap("connections", Collections.singletonList("*")));
          ObserverSubscription top = ObserverRuntime.subscribe("sql_top", "spring-jdbc-top", Collections.emptyMap())) {
        assertEquals("private-initial-value", jdbc.queryForObject("SELECT payload FROM spring_orders WHERE id=?", String.class, 7L));
        assertEquals("private-initial-value", named.queryForObject("SELECT payload FROM spring_orders WHERE id=:id", Collections.singletonMap("id", 7L), String.class));
        transaction.execute(status -> {
          assertEquals(1, named.update("UPDATE spring_orders SET payload=:payload WHERE id=:id", parameters()));
          status.setRollbackOnly();
          return null;
        });
        assertEquals("private-initial-value", jdbc.queryForObject("SELECT payload FROM spring_orders WHERE id=?", String.class, 7L));
        List<Map<String, Object>> rows = capture.drain();
        List<Map<String, Object>> orders = new ArrayList<>();
        for (Map<String, Object> row : rows) if (String.valueOf(row.get("sql")).toLowerCase(Locale.ROOT).contains("spring_orders")) orders.add(row);
        assertEquals("pool delegates must not duplicate logical executions", 4, orders.size());
        Set<Object> identities = new HashSet<>();
        for (Map<String, Object> row : orders) {
          assertEquals("jdbc", row.get("api"));
          assertTrue(identities.add(row.get("operationId")));
          assertNotNull(row.get("startedAt"));
          assertTrue(((Number) row.get("durationNs")).longValue() >= 0);
          assertFalse(row.toString().contains("private-initial-value"));
          assertFalse(row.toString().contains("private-rolled-back-value"));
          assertFalse(row.containsKey("error"));
        }
        assertTrue(top.snapshot().isEmpty());
      }
    }
  }

  private static Map<String, Object> parameters() {
    Map<String, Object> values = new LinkedHashMap<>();
    values.put("id", 7L);
    values.put("payload", "private-rolled-back-value");
    return values;
  }
}
