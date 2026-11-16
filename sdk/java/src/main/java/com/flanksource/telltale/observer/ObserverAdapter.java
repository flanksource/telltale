package com.flanksource.telltale.observer;

import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.Set;

/** A protocol observer shares one reversible transformer and never owns application configuration. */
public interface ObserverAdapter {
  String id();
  Set<String> kinds();
  boolean matchesType(String name, Set<String> hierarchy);
  boolean matchesMethod(String name, String descriptor);
  Object onEnter(String owner, String name, String descriptor, Object receiver, Object[] args);
  Object onExit(Object token, Object result, Throwable error);

  default boolean matchesInvocation(String owner, String name, String descriptor) { return false; }
  default void onOperation(Map<String, Object> metadata) {}
  default List<Map<String, Object>> snapshot(String kind) { return Collections.emptyList(); }
  default void onDisarm(String kind) {}
  default Map<String, Object> project(String kind, Map<String, Object> params, Map<String, Object> row) { return row; }
  default void validate(String kind, Map<String, Object> params) {}
  default List<Map<String, Object>> terminalRows(String kind, Map<String, Object> params) { return Collections.emptyList(); }
}
