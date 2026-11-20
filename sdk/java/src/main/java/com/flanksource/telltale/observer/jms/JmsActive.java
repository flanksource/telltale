package com.flanksource.telltale.observer.jms;

import java.util.*;

final class JmsActive {
  private final Set<JmsOperation> operations = Collections.newSetFromMap(new java.util.concurrent.ConcurrentHashMap<>());
  private final int maximum;

  JmsActive(int maximum) { this.maximum = maximum; }

  synchronized boolean add(JmsOperation operation) {
    if (operations.size() >= maximum && !operations.contains(operation)) return false;
    operations.add(operation);
    return true;
  }

  void remove(JmsOperation operation) { operations.remove(operation); }
  void clear() { operations.clear(); }

  List<Map<String, Object>> terminalRows() {
    List<Map<String, Object>> rows = new ArrayList<>();
    for (JmsOperation operation : operations) {
      Map<String, Object> row = operation.incomplete();
      if (row != null) rows.add(row);
    }
    return rows;
  }
}
