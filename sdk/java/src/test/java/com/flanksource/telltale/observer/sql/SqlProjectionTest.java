package com.flanksource.telltale.observer.sql;

import static org.junit.jupiter.api.Assertions.*;

import java.util.*;
import org.junit.jupiter.api.Test;

class SqlProjectionTest {
  @Test
  void rejectsTheGoOnlySelfConnectionWithActionableJvmGuidance() {
    IllegalArgumentException failure = assertThrows(IllegalArgumentException.class,
        () -> SqlProjection.validate("sql", Collections.singletonMap("connections", Collections.singletonList("self"))));
    assertTrue(failure.getMessage().contains("use *"));
  }

  @Test
  void filtersEachCaptureIndependentlyByConnectionAndDuration() {
    Map<String, Object> record = new HashMap<>();
    record.put("connection", "orders");
    record.put("durationNs", 1000L);
    Map<String, Object> params = new HashMap<>();
    params.put("connections", Collections.singletonList("orders"));
    params.put("minDurationNs", 1000L);
    assertSame(record, SqlProjection.project("sql", params, record));
    params.put("minDurationNs", 1001L);
    assertNull(SqlProjection.project("sql", params, record));
    params.put("minDurationNs", 0L);
    params.put("connections", Collections.singletonList("billing"));
    assertNull(SqlProjection.project("sql", params, record));
  }

  @Test
  void filtersLiveOperationsBeforeTheRuntimeAppliesTheTopLimit() {
    Map<String, Object> details = new HashMap<>();
    details.put("sql", "SELECT id FROM order_ledger");
    details.put("database", "orders");
    Map<String, Object> row = new HashMap<>();
    row.put("attributes", details);
    Map<String, Object> params = new HashMap<>();
    params.put("databases", Arrays.asList("orders*", "!orders_archive"));
    params.put("text", Collections.singletonList("LEDGER"));
    assertSame(row, SqlProjection.project("sql_top", params, row));
    params.put("text", Collections.singletonList("!ledger"));
    assertNull(SqlProjection.project("sql_top", params, row));
  }
}
