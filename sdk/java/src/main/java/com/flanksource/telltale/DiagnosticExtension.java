package com.flanksource.telltale;

/** Implementations are discovered from extension JAR service descriptors. */
public interface DiagnosticExtension extends AutoCloseable {
  String id();

  String version();

  default int apiVersion() {
    return 1;
  }

  void start(ExtensionContext context) throws Exception;

  void close() throws Exception;
}
