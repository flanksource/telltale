package com.flanksource.telltale.observer.sql;

import io.opentelemetry.instrumentation.api.incubator.semconv.db.SqlDialect;
import io.opentelemetry.instrumentation.api.incubator.semconv.db.SqlQueryAnalyzer;

final class SqlText {
  private static final SqlQueryAnalyzer ANALYZER = SqlQueryAnalyzer.create(true);
  private SqlText() {}
  static String sanitize(String sql) {
    if (sql.length() > 65536) throw new IllegalArgumentException("SQL text exceeds the 64 KiB observer limit");
    return ANALYZER.analyze(sql, SqlDialect.DOUBLE_QUOTES_ARE_STRING_LITERALS).getQueryText();
  }
}
