package com.flanksource.telltale.observer.sql;

import static org.junit.jupiter.api.Assertions.*;

import java.sql.Statement;
import org.junit.jupiter.api.Test;

class SqlRowsTest {
  @Test
  void preservesKnownUpdatesAndOnlyFullyKnownBatchCounts() {
    assertEquals(3L, SqlRows.observed("executeUpdate", 3));
    assertEquals(7L, SqlRows.observed("executeLargeUpdate", 7L));
    assertEquals(5L, SqlRows.observed("executeBatch", new int[] {2, 3}));
    assertEquals(9L, SqlRows.observed("executeLargeBatch", new long[] {4, 5}));
    assertEquals(0L, SqlRows.observed("executeBatch", new int[0]));
    assertEquals(-1L, SqlRows.observed("executeBatch", new int[] {Statement.SUCCESS_NO_INFO, 3}));
    assertEquals(-1L, SqlRows.observed("executeLargeBatch", new long[] {3, Statement.EXECUTE_FAILED}));
    assertEquals(-1L, SqlRows.observed("execute", true));
    assertEquals(-1L, SqlRows.observed("executeQuery", null));
    assertThrows(ArithmeticException.class, () -> SqlRows.observed("executeLargeBatch", new long[] {Long.MAX_VALUE, 1}));
  }
}
