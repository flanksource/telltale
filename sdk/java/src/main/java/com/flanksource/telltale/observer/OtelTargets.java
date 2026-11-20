package com.flanksource.telltale.observer;

import java.io.IOException;
import java.io.InputStream;
import java.util.*;

/** Foreign ABI names are data; shading affects our implementation without rewriting its targets. */
final class OtelTargets {
  private static final Properties NAMES = load();
  private OtelTargets() {}

  static String name(String key) {
    String name = NAMES.getProperty(key);
    if (name == null || name.trim().isEmpty())
      throw new IllegalStateException("missing observer ABI target " + key);
    return name;
  }

  static Map<String, Object> catalog() {
    Map<String, Object> result = new LinkedHashMap<>();
    Map<String, String> versions = new LinkedHashMap<>();
    for (String key : Arrays.asList("instrumentation.version", "sdk.version", "profiles.version", "protobuf.version"))
      versions.put(key, name(key));
    result.put("certifiedPins", versions);
    result.put("compatibility", name("compatibility"));
    Map<String, Map<String, String>> adapters = new TreeMap<>();
    for (String key : new TreeSet<>(NAMES.stringPropertyNames())) {
      if (!key.startsWith("capability.")) continue;
      String path = key.substring("capability.".length());
      int separator = path.indexOf('.');
      if (separator <= 0) throw new IllegalStateException("invalid observer capability key " + key);
      adapters.computeIfAbsent(path.substring(0, separator), unused -> new LinkedHashMap<>())
          .put(path.substring(separator + 1), name(key));
    }
    result.put("adapters", adapters);
    return result;
  }

  private static Properties load() {
    Properties properties = new Properties();
    try (InputStream input = OtelTargets.class.getResourceAsStream("targets.properties")) {
      if (input == null) throw new IllegalStateException("observer ABI target catalog is missing");
      properties.load(input);
      return properties;
    } catch (IOException error) { throw new IllegalStateException("read observer ABI target catalog", error); }
  }
}
