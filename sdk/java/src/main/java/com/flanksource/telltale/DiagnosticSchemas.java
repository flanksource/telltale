package com.flanksource.telltale;

import java.util.*;

final class DiagnosticSchemas {
  /** A built-in operation's description and the CLI/API command it is served as. */
  private static final Map<String, String[]> DOCS = new HashMap<>();

  private static void doc(String name, String command, String description) {
    DOCS.put(name, new String[] {command, description});
  }

  static {
    doc("jvm.settings", "jvm settings", "Read OS/JVM properties, environment and resource settings");
    doc("jvm.resources", "jvm resources", "Read filesystem capacity and Linux process and cgroup resources");
    doc("jvm.flags", "jvm flags", "Read effective HotSpot VM flags");
    doc("jvm.extensions.list", "jvm extensions list", "List loaded diagnostic extensions");
    doc("jvm.extensions.install", "jvm extensions install", "Install a diagnostic extension JAR");
    doc("jvm.extensions.replace", "jvm extensions replace", "Replace a loaded diagnostic extension with a new JAR");
    doc("jvm.extensions.unload", "jvm extensions unload", "Unload a diagnostic extension");
    doc("jvm.catalog", "jvm catalog", "List the operations this runtime supports");
    doc("jvm.status", "jvm status", "Report mutation permission, instrumentation and active patches");
    doc("jvm.jmx.search", "jvm jmx search", "Search MBean names matching an ObjectName pattern");
    doc("jvm.jmx.info", "jvm jmx info", "Describe an MBean's attributes, operations and notifications");
    doc("jvm.jmx.read", "jvm jmx read", "Read one MBean attribute, or all of them");
    doc("jvm.jmx.write", "jvm jmx write", "Write an MBean attribute");
    doc("jvm.jmx.invoke", "jvm jmx invoke", "Invoke an MBean operation");
    doc("jvm.metrics", "jvm metrics", "Snapshot memory, GC, thread, class-loading and runtime metrics");
    doc("jvm.threads", "jvm threads", "Sample thread states and CPU over a short interval");
    doc("jvm.threads.dump", "jvm threads dump", "Dump every thread's stack as text");
    doc("jvm.threads.stack", "jvm threads stack", "Read one thread's stack and lock state");
    doc("jvm.heap.histogram", "jvm heap histogram", "List the heap's classes by retained bytes (GC.class_histogram)");
    doc("jvm.logging.list", "jvm logging list", "List java.util.logging loggers and their levels");
    doc("jvm.logging.set", "jvm logging set", "Set a logger's level, or inherit its parent's with null");
    doc("jvm.classes", "jvm classes", "Page through loaded classes by name and loader");
    doc("jvm.loaders", "jvm loaders", "List class loaders and their identities");
    doc("jvm.groovy.eval", "jvm groovy eval", "Execute a Groovy script once in an application loader");
    doc("jvm.ognl.eval", "jvm ognl eval", "Evaluate an OGNL expression in an application loader");
    doc("jvm.classes.source", "jvm decompile", "Decompile a class with its descriptors and line mappings");
    doc("jvm.classes.bytecode", "jvm decompile bytecode", "Read a class's bytecode as base64");
    doc("jvm.classes.compile", "jvm patch compile", "Preview compiling replacement source without applying it");
    doc("jvm.classes.patch", "jvm patch apply", "Compile and apply replacement source under a restore lease");
    doc("jvm.classes.restore", "jvm patch restore", "Restore a class's original bytecode, or remove one probe");
    doc("jvm.classes.lease", "jvm patch lease", "Renew or change a patched class's restore lease");
    doc("jvm.classes.patches", "jvm patch list", "List active bytecode patches and probes");
    doc("jvm.classes.instrument", "jvm trace instrument", "Add a method probe to a class under a restore lease");
    doc("jvm.probes.list", "jvm trace probes", "List method probe keys");
    doc("jvm.probes.read", "jvm trace read", "Read buffered events of a method probe");
    doc("jvm.probes.freeze", "jvm trace freeze", "Stop a method probe from recording further events");
    doc("jvm.notifications.list", "jvm notifications list", "List JMX notification subscriptions and their queues");
  }

  private static String[] docs(String name) {
    String[] docs = DOCS.get(name);
    if (docs == null) throw new IllegalStateException("operation has no command or description: " + name);
    return docs;
  }

  static String command(String name) {
    return docs(name)[0];
  }

  static String description(String name) {
    return docs(name)[1];
  }

  static Map<String, Object> result(String name) {
    Map<String, Object> schema = new LinkedHashMap<>();
    if (name.equals("jvm.jmx.read") || name.equals("jvm.jmx.invoke")) return schema;
    if (name.equals("jvm.threads.dump") || name.equals("jvm.flags")) {
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
    if (name.equals("jvm.heap.histogram")) {
      Map<String, Object> properties = new LinkedHashMap<>();
      add(properties, "collectedAt", "string");
      add(properties, "live", "boolean");
      add(properties, "totalInstances", "integer");
      add(properties, "totalBytes", "integer");
      add(properties, "matched", "integer");
      add(properties, "rows", "array");
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
        level.put("type", "string");
        level.put("nullable", true);
        level.put("title", "level");
        level.put("description", "Use null to inherit the parent logger level.");
        properties.put("level", level);
        required.add("level");
        break;
      case "jvm.threads":
        add(properties, "sampleMs", "integer");
        break;
      case "jvm.heap.histogram":
        add(properties, "top", "integer");
        add(properties, "classPattern", "string");
        add(properties, "live", "boolean");
        describe(properties, "top", "Rows to return, largest first (default 100).");
        describe(properties, "classPattern", "Glob over class names, e.g. com.example.*");
        describe(
            properties, "live", "Count only reachable objects. Forces a full GC on this JVM first.");
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

  @SuppressWarnings("unchecked")
  private static void describe(Map<String, Object> properties, String name, String description) {
    ((Map<String, Object>) properties.get(name)).put("description", description);
  }

  private static void add(Map<String, Object> properties, String name, String type) {
    Map<String, Object> schema = new LinkedHashMap<>();
    schema.put("type", type);
    schema.put("title", name);
    if (name.equals("source") || name.equals("expression")) schema.put("format", "textarea");
    if (type.equals("array"))
      schema.put(
          "items",
          name.equals("signature")
              ? Collections.singletonMap("type", "string")
              : Collections.emptyMap());
    if (type.equals("object")) schema.put("additionalProperties", Collections.emptyMap());
    if (name.equals("jar")) schema.put("format", "byte");
    properties.put(name, schema);
  }
}
