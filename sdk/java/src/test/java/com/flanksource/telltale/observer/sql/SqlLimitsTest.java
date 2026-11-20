package com.flanksource.telltale.observer.sql;

import static org.junit.jupiter.api.Assertions.*;

import java.sql.*;
import java.util.*;
import java.util.concurrent.*;
import org.junit.jupiter.api.Test;

class SqlLimitsTest {
  @Test
  void metadataCapacityFailsExplicitlyWhileUpdatesRemainValid() {
    SqlMetadata metadata = new SqlMetadata(1);
    Object first = new Object(), second = new Object();
    metadata.put(first, Collections.singletonMap("sql", "SELECT ?"));
    metadata.put(first, Collections.singletonMap("sql", "SELECT id FROM orders"));
    assertThrows(IllegalStateException.class, () -> metadata.put(second, Collections.emptyMap()));
    assertEquals("SELECT id FROM orders", metadata.get(first).get("sql"));
  }

  @Test
  void activeCapacityFailsExplicitlyAndReleasesAfterTheOriginalExecution() throws Exception {
    SqlObserver.Options options = new SqlObserver.Options(row -> {}, () -> true);
    options.maxInflight = 1;
    SqlObserver observer = new SqlObserver(options);
    CountDownLatch entered = new CountDownLatch(1), finish = new CountDownLatch(1);
    try (Connection connection = DriverManager.getConnection("jdbc:h2:mem:sql-limit");
        Statement statement = connection.createStatement()) {
      ExecutorService worker = Executors.newSingleThreadExecutor();
      try {
        Future<?> first = worker.submit(() -> {
          Object token = observer.onEnter("test", "execute", "", statement, new Object[] {"SELECT 1"});
          entered.countDown();
          try { assertTrue(finish.await(10, TimeUnit.SECONDS)); }
          catch (InterruptedException failure) { Thread.currentThread().interrupt(); throw new IllegalStateException(failure); }
          observer.onExit(token, null, null);
        });
        assertTrue(entered.await(10, TimeUnit.SECONDS));
        assertThrows(IllegalStateException.class, () -> observer.onEnter("test", "execute", "", statement, new Object[] {"SELECT 2"}));
        finish.countDown();
        first.get(10, TimeUnit.SECONDS);
        Object token = observer.onEnter("test", "execute", "", statement, new Object[] {"SELECT 3"});
        observer.onExit(token, null, null);
        assertTrue(observer.snapshot("sql_top").isEmpty());
      } finally {
        finish.countDown();
        worker.shutdownNow();
      }
    }
  }
}
