package com.flanksource.telltale.diag.jmx;

import java.util.ArrayList;
import java.util.Enumeration;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.logging.Level;
import java.util.logging.LogManager;
import java.util.logging.Logger;

public final class LoggingService {
  public List<Map<String, Object>> loggers() {
    List<Map<String, Object>> result = new ArrayList<>();
    LogManager manager = LogManager.getLogManager();
    Enumeration<String> names = manager.getLoggerNames();
    while (names.hasMoreElements()) {
      String name = names.nextElement();
      Logger logger = manager.getLogger(name);
      // JUL keeps weak references; a logger may disappear during enumeration.
      if (logger == null) continue;
      Map<String, Object> row = new LinkedHashMap<>();
      row.put("name", name);
      row.put("level", logger.getLevel() == null ? null : logger.getLevel().getName());
      row.put("parent", logger.getParent() == null ? null : logger.getParent().getName());
      result.add(row);
    }
    result.sort((left, right) -> ((String) left.get("name")).compareTo((String) right.get("name")));
    return result;
  }

  public void setLevel(String name, String level) {
    if (name == null)
      throw new IllegalArgumentException("logger name is required (empty names the root)");
    Logger logger = LogManager.getLogManager().getLogger(name);
    if (logger == null) throw new IllegalArgumentException("Unknown logger: " + name);
    logger.setLevel(level == null ? null : Level.parse(level));
  }
}
