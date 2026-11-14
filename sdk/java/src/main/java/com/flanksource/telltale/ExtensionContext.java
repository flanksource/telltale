package com.flanksource.telltale;

import java.lang.management.ManagementFactory;
import java.util.*;
import javax.management.MBeanServer;

public final class ExtensionContext implements AutoCloseable {
  public interface Bindings {
    Map<String, Object> forLoader(ClassLoader application) throws Exception;
  }

  final Map<String, OperationSpec> operations = new LinkedHashMap<>();
  private final List<AutoCloseable> resources = new ArrayList<>();
  private Bindings bindings = application -> Collections.emptyMap();

  public void register(OperationSpec operation) {
    if (operation.handler == null)
      throw new IllegalArgumentException("operation handler is required: " + operation.name);
    if (operations.putIfAbsent(operation.name, operation) != null)
      throw new IllegalArgumentException("duplicate operation: " + operation.name);
  }

  public void bindings(Bindings provider) {
    bindings = Objects.requireNonNull(provider);
  }

  public void own(AutoCloseable resource) {
    resources.add(Objects.requireNonNull(resource));
  }

  public MBeanServer mbeans() {
    return ManagementFactory.getPlatformMBeanServer();
  }

  Map<String, Object> bindings(ClassLoader application) throws Exception {
    return bindings.forLoader(application);
  }

  public void close() throws Exception {
    Exception failure = null;
    ListIterator<AutoCloseable> iterator = resources.listIterator(resources.size());
    while (iterator.hasPrevious()) {
      try {
        iterator.previous().close();
        iterator.remove();
      } catch (Exception error) {
        if (failure == null) failure = error;
        else failure.addSuppressed(error);
      }
    }
    if (failure != null) throw failure;
  }
}
