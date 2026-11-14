package com.flanksource.telltale;

import groovy.lang.Binding;
import groovy.lang.GroovyShell;
import groovy.transform.ThreadInterrupt;
import groovy.transform.TimedInterrupt;
import java.io.*;
import java.util.*;
import org.codehaus.groovy.control.CompilerConfiguration;
import org.codehaus.groovy.control.customizers.ASTTransformationCustomizer;

public final class GroovyEngine {
  public static Map<String, Object> evaluate(
      Map<String, Object> params, ClassLoader loader, Object jmx, Map<String, Object> extensions)
      throws Exception {
    Object configured = params.getOrDefault("timeoutSeconds", 30);
    if (!(configured instanceof Number))
      throw new IllegalArgumentException("timeoutSeconds must be an integer");
    long timeout;
    try {
      timeout = new java.math.BigDecimal(configured.toString()).longValueExact();
    } catch (ArithmeticException | NumberFormatException error) {
      throw new IllegalArgumentException("timeoutSeconds must be an integer", error);
    }
    if (timeout <= 0 || timeout > 3600)
      throw new IllegalArgumentException("timeoutSeconds must be between one and 3600");
    CompilerConfiguration compiler = new CompilerConfiguration();
    compiler.addCompilationCustomizers(
        new ASTTransformationCustomizer(
            Collections.singletonMap("value", timeout), TimedInterrupt.class));
    compiler.addCompilationCustomizers(new ASTTransformationCustomizer(ThreadInterrupt.class));
    BoundedWriter output = new BoundedWriter();
    BoundedWriter error = new BoundedWriter();
    Binding bindings = new Binding();
    bindings.setVariable("args", params.getOrDefault("args", Collections.emptyMap()));
    bindings.setVariable("out", new PrintWriter(output));
    bindings.setVariable("err", new PrintWriter(error));
    bindings.setVariable("jmx", jmx);
    bindings.setVariable("application", loader);
    bindings.setVariable("extensions", extensions);
    ClassLoader previous = Thread.currentThread().getContextClassLoader();
    GroovyShell shell = new GroovyShell(GroovyEngine.class.getClassLoader(), bindings, compiler);
    long start = System.nanoTime();
    try {
      Thread.currentThread().setContextClassLoader(loader);
      Object value = shell.evaluate((String) params.get("source"));
      Map<String, Object> result = new LinkedHashMap<>();
      if (value == null
          || value instanceof String
          || value instanceof Number
          || value instanceof Boolean) result.put("value", value);
      else
        result.put(
            "graph",
            new com.flanksource.telltale.diag.graph.ValueGraph(
                    com.flanksource.telltale.diag.graph.GraphLimits.probe(
                        4, 100, 1000, 4096, 1048576))
                .root(value, "result", null));
      result.put("stdout", output.toString());
      result.put("stderr", error.toString());
      result.put("elapsedMs", (System.nanoTime() - start) / 1000000);
      return result;
    } finally {
      Thread.currentThread().setContextClassLoader(previous);
      shell.getClassLoader().clearCache();
      shell.getClassLoader().close();
    }
  }

  private static final class BoundedWriter extends Writer {
    private final StringBuilder text = new StringBuilder();

    public void write(char[] chars, int offset, int length) {
      if (length > 1048576 - text.length())
        throw new IllegalStateException("script output exceeds 1 MiB");
      text.append(chars, offset, length);
    }

    public void flush() {}

    public void close() {}

    public String toString() {
      return text.toString();
    }
  }
}
