package observerfixture.springsql;

import static org.junit.Assert.*;

import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverSubscription;
import com.zaxxer.hikari.HikariConfig;
import com.zaxxer.hikari.HikariDataSource;
import java.util.*;
import javax.sql.DataSource;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.springframework.jdbc.core.JdbcTemplate;

public final class SpringJdbcClientFixture {
  public static void existingClient() throws Exception {
    HikariConfig config = new HikariConfig();
    config.setJdbcUrl("jdbc:h2:mem:spring-jdbc-client-" + UUID.randomUUID());
    config.setMaximumPoolSize(1);
    try (HikariDataSource pool = new HikariDataSource(config)) {
      JdbcTemplate jdbc = new JdbcTemplate(pool);
      jdbc.execute("CREATE TABLE spring_client_orders(id BIGINT PRIMARY KEY,payload VARCHAR(100))");
      jdbc.update("INSERT INTO spring_client_orders(id,payload) VALUES(?,?)", 7L, "private-jdbc-client-value");
      Class<?> type = SpringJdbcClientFixture.class.getClassLoader().loadClass("org.springframework.jdbc.core.simple.JdbcClient");
      Object existing = type.getMethod("create", DataSource.class).invoke(null, pool);
      assertEquals("private-jdbc-client-value", request(existing));
      ObserverRuntime.install(ByteBuddyAgent.install());
      try (ObserverSubscription capture = ObserverRuntime.subscribe("sql", "spring-jdbc-client", Collections.singletonMap("connections", Collections.singletonList("*")))) {
        assertEquals("private-jdbc-client-value", request(existing));
        List<Map<String, Object>> rows = capture.drain();
        assertEquals("JdbcClient must be observed through its single JDBC execution", 1, rows.size());
        Map<String, Object> row = rows.get(0);
        assertEquals("jdbc", row.get("api"));
        assertTrue(String.valueOf(row.get("sql")).toLowerCase(Locale.ROOT).contains("spring_client_orders"));
        assertFalse(row.containsKey("error"));
        assertFalse(row.toString().contains("private-jdbc-client-value"));
      }
    }
  }

  private static String request(Object existing) throws Exception {
    ClassLoader loader = SpringJdbcClientFixture.class.getClassLoader();
    Class<?> type = loader.loadClass("org.springframework.jdbc.core.simple.JdbcClient");
    Object statement = type.getMethod("sql", String.class).invoke(existing, "SELECT payload FROM spring_client_orders WHERE id=:id");
    Class<?> spec = loader.loadClass("org.springframework.jdbc.core.simple.JdbcClient$StatementSpec");
    statement = spec.getMethod("param", String.class, Object.class).invoke(statement, "id", 7L);
    Object query = spec.getMethod("query", Class.class).invoke(statement, String.class);
    Class<?> mapped = loader.loadClass("org.springframework.jdbc.core.simple.JdbcClient$MappedQuerySpec");
    return (String) mapped.getMethod("single").invoke(query);
  }
}
