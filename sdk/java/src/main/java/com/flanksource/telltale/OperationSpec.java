package com.flanksource.telltale;

import java.util.*;

/** JDK-only operation definition shared by extensions and all transports. */
public final class OperationSpec {
  public interface Handler {
    Object call(Map<String, Object> parameters) throws Exception;
  }

  final String name;
  String title;
  String description = "";
  String command;
  Map<String, Object> parameters = Collections.singletonMap("type", "object");
  Map<String, Object> result = Collections.emptyMap();
  boolean mutates;
  Handler handler;

  private OperationSpec(String name) {
    if (name == null || name.trim().isEmpty())
      throw new IllegalArgumentException("operation name is required");
    this.name = name;
    this.title = name;
  }

  public static OperationSpec named(String name) {
    return new OperationSpec(name);
  }

  public OperationSpec title(String value) {
    title = Objects.requireNonNull(value);
    return this;
  }

  public OperationSpec description(String value) {
    description = Objects.requireNonNull(value);
    return this;
  }

  /** The space-separated CLI command, and API path, a built-in operation is served as. */
  OperationSpec command(String value) {
    command = Objects.requireNonNull(value);
    return this;
  }

  public OperationSpec parameters(Map<String, Object> schema) {
    parameters = new LinkedHashMap<>(schema);
    return this;
  }

  public OperationSpec result(Map<String, Object> schema) {
    result = new LinkedHashMap<>(schema);
    return this;
  }

  public OperationSpec mutates(boolean value) {
    mutates = value;
    return this;
  }

  public OperationSpec handle(Handler value) {
    handler = Objects.requireNonNull(value);
    return this;
  }

  Map<String, Object> descriptor() {
    Map<String, Object> value = new LinkedHashMap<>();
    value.put("name", name);
    value.put("title", title);
    value.put("description", description);
    if (command != null) value.put("command", command);
    value.put("paramsSchema", parameters);
    value.put("resultSchema", result);
    value.put("mutates", mutates);
    return value;
  }
}
