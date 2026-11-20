package com.flanksource.telltale.observer.sql;

final class SqlRows {
  private SqlRows() {}

  static long observed(String method, Object result) {
    if ("executeUpdate".equals(method) || "executeLargeUpdate".equals(method)) {
      if (!(result instanceof Number)) throw new IllegalArgumentException(method + " returned no numeric row count");
      return ((Number) result).longValue() < 0 ? -1L : ((Number) result).longValue();
    }
    if (!"executeBatch".equals(method) && !"executeLargeBatch".equals(method)) return -1L;
    long total = 0;
    if (result instanceof int[]) {
      for (int count : (int[]) result) { if (count < 0) return -1L; total = Math.addExact(total, count); }
    } else if (result instanceof long[]) {
      for (long count : (long[]) result) { if (count < 0) return -1L; total = Math.addExact(total, count); }
    } else throw new IllegalArgumentException(method + " returned no batch row counts");
    return total;
  }
}
