package com.flanksource.telltale;

import java.util.*;

final class DiagnosticSchemas {
  static Map<String, Object> result(String name) {
    Map<String, Object> schema = new LinkedHashMap<>();
    if (name.equals("jvm.jmx.read") || name.equals("jvm.jmx.invoke")) return schema;
    if (name.equals("jvm.threads.dump")) {
      schema.put("type", "string");
      return schema;
    }
    boolean array =
        Arrays.asList(
                "jvm.jmx.search",
                "jvm.logging.list",
                "jvm.loaders",
                "jvm.classes.patches",
                "jvm.extensions.list",
                "jvm.catalog",
                "jvm.notifications.list")
            .contains(name);
    schema.put("type", array ? "array" : "object");
    if (array)
      schema.put(
          "items",
          Collections.singletonMap("type", name.equals("jvm.jmx.search") ? "string" : "object"));
    if (name.equals("jvm.groovy.eval") || name.equals("jvm.ognl.eval")) {
      Map<String, Object> properties = new LinkedHashMap<>();
      properties.put("value", Collections.emptyMap());
      add(properties, "graph", "object");
      if (name.contains("groovy")) {
        add(properties, "stdout", "string");
        add(properties, "stderr", "string");
        add(properties, "elapsedMs", "integer");
      }
      schema.put("properties", properties);
    }
    return schema;
  }

  static Map<String, Object> parameters(String name) {
    Map<String, Object> properties = new LinkedHashMap<>();
    List<String> required = new ArrayList<>();
    switch (name) {
      case "jvm.jmx.search":
        add(properties, "pattern", "string");
        break;
      case "jvm.jmx.info":
      case "jvm.jmx.read":
      case "jvm.jmx.write":
      case "jvm.jmx.invoke":
        field(properties, required, "objectName", "string");
        if (name.endsWith("read")) add(properties, "attribute", "string");
        if (name.endsWith("write")) {
          field(properties, required, "attribute", "string");
          properties.put("value", Collections.emptyMap());
          required.add("value");
        }
        if (name.endsWith("invoke")) {
          field(properties, required, "operation", "string");
          add(properties, "args", "array");
          add(properties, "signature", "array");
        }
        break;
      case "jvm.groovy.eval":
      case "jvm.ognl.eval":
        field(properties, required, name.contains("groovy") ? "source" : "expression", "string");
        add(properties, "loader", "string");
        if (name.contains("groovy")) {
          add(properties, "args", "object");
          add(properties, "timeoutSeconds", "integer");
        }
        break;
      case "jvm.classes":
        add(properties, "query", "string");
        add(properties, "loader", "string");
        add(properties, "limit", "integer");
        add(properties, "offset", "integer");
        add(properties, "includePlatform", "boolean");
        break;
      case "jvm.classes.source":
      case "jvm.classes.bytecode":
      case "jvm.classes.compile":
      case "jvm.classes.patch":
      case "jvm.classes.instrument":
      case "jvm.classes.restore":
      case "jvm.classes.lease":
        field(properties, required, "class", "string");
        add(properties, "loader", "string");
        add(properties, "method", "string");
        if (name.endsWith("compile") || name.endsWith("patch"))
          field(properties, required, "source", "string");
        if (name.endsWith("compile") || name.endsWith("patch"))
          add(properties, "classpath", "string");
        if (name.endsWith("instrument")) field(properties, required, "probe", "object");
        if (name.endsWith("restore")) add(properties, "probe", "object");
        if (name.endsWith("lease")) add(properties, "seconds", "integer");
        if (name.endsWith("patch") || name.endsWith("instrument"))
          add(properties, "leaseSeconds", "integer");
        break;
      case "jvm.probes.read":
        field(properties, required, "key", "string");
        add(properties, "since", "integer");
        add(properties, "limit", "integer");
        break;
      case "jvm.probes.freeze":
        field(properties, required, "key", "string");
        break;
      case "jvm.logging.set":
        field(properties, required, "name", "string");
        Map<String, Object> level = new LinkedHashMap<>();
        level.put("type", Arrays.asList("string", "null"));
        level.put("title", "level");
        level.put("description", "Use null to inherit the parent logger level.");
        properties.put("level", level);
        required.add("level");
        break;
      case "jvm.threads":
        add(properties, "sampleMs", "integer");
        break;
      case "jvm.threads.stack":
        field(properties, required, "id", "integer");
        break;
      case "jvm.extensions.install":
      case "jvm.extensions.replace":
        field(properties, required, "jar", "string");
        break;
      case "jvm.extensions.unload":
        field(properties, required, "id", "string");
        break;
      default:
        break;
    }
    Map<String, Object> schema = new LinkedHashMap<>();
    schema.put("type", "object");
    schema.put("properties", properties);
    schema.put("required", required);
    schema.put("additionalProperties", false);
    return schema;
  }

  private static void field(
      Map<String, Object> properties, List<String> required, String name, String type) {
    add(properties, name, type);
    required.add(name);
  }

  private static void add(Map<String, Object> properties, String name, String type) {
    Map<String, Object> schema = new LinkedHashMap<>();
    schema.put("type", type);
    schema.put("title", name);
    if (name.equals("source") || name.equals("expression")) schema.put("format", "textarea");
    if (type.equals("array")) schema.put("items", Collections.emptyMap());
    properties.put(name, schema);
  }
}
