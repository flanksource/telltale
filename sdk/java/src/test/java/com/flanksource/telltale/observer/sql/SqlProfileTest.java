package com.flanksource.telltale.observer.sql;

import static org.junit.jupiter.api.Assertions.*;

import java.sql.*;
import java.util.*;
import org.junit.jupiter.api.Test;

class SqlProfileTest {
  @Test
  @SuppressWarnings("unchecked")
  void boundsProfileSqlWhileKeepingFullTextFiltersAndCompletedRecords() throws Exception {
    List<Map<String, Object>> rows = new ArrayList<>();
    SqlObserver observer = new SqlObserver(new SqlObserver.Options(rows::add, () -> true));
    List<String> columns = new ArrayList<>();
    for (int index = 0; index < 200; index++) columns.add("column_" + index);
    String sql = "SELECT " + String.join(",", columns) + " FROM orders WHERE future_filter_column = 17";
    try (Connection connection = DriverManager.getConnection("jdbc:h2:mem:sql-profile");
        Statement statement = connection.createStatement()) {
      Object token = observer.onEnter("test", "execute", "", statement, new Object[] {sql});
      Map<String, Object> sample = observer.snapshot("sql_top").get(0);
      Map<String, Object> attrs = (Map<String, Object>) sample.get("attributes");
      assertTrue(attrs.get("sql").toString().length() <= 1024);
      assertEquals(true, attrs.get("sqlTruncated"));
      assertEquals(64, attrs.get("queryFingerprint").toString().length());
      assertFalse(attrs.containsKey("batchSql"));
      Map<String, Object> projected = observer.project("sql_top", Collections.singletonMap("text", Collections.singletonList("future_filter_column")), sample);
      assertNotNull(projected);
      assertTrue(projected.keySet().stream().noneMatch(key -> key.startsWith("telltale.")));
      observer.onExit(token, null, null);
      assertTrue(rows.get(0).get("sql").toString().length() > 1024);
    }
  }
}
