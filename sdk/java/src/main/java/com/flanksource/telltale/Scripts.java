package com.flanksource.telltale;

import com.flanksource.telltale.diag.jmx.JmxService;
import java.lang.reflect.InvocationTargetException;
import java.net.*;
import java.util.*;

final class Scripts {
  @SuppressWarnings("unchecked")
  static Map<String, Object> evaluate(
      Map<String, Object> params,
      ClassLoader application,
      JmxService jmx,
      Map<String, Object> extensions)
      throws Exception {
    Diagnostics.required(params, "source");
    if (params.containsKey("args") && !(params.get("args") instanceof Map))
      throw new IllegalArgumentException("args must be an object");
    URL engine = Scripts.class.getProtectionDomain().getCodeSource().getLocation();
    URL groovy = groovy.lang.GroovyShell.class.getProtectionDomain().getCodeSource().getLocation();
    ClassLoader runtime = Scripts.class.getClassLoader();
    try (URLClassLoader isolated =
        new URLClassLoader(new URL[] {engine, groovy}, null) {
          protected synchronized Class<?> loadClass(String name, boolean resolve)
              throws ClassNotFoundException {
            Class<?> type = findLoadedClass(name);
            if (type == null) {
              if (name.startsWith("groovy.")
                  || name.startsWith("groovyjarjar")
                  || name.startsWith("org.codehaus.groovy.")
                  || name.startsWith("org.apache.groovy.")
                  || name.startsWith("com.flanksource.telltale.GroovyEngine"))
                type = findClass(name);
              else if (name.startsWith("com.flanksource.telltale.")) type = runtime.loadClass(name);
              else
                try {
                  type =
                      application == null
                          ? Class.forName(name, false, null)
                          : application.loadClass(name);
                } catch (ClassNotFoundException absent) {
                  type = runtime.loadClass(name);
                }
            }
            if (resolve) resolveClass(type);
            return type;
          }
        }) {
      try {
        return (Map<String, Object>)
            isolated
                .loadClass("com.flanksource.telltale.GroovyEngine")
                .getMethod("evaluate", Map.class, ClassLoader.class, Object.class, Map.class)
                .invoke(null, params, application, jmx, extensions);
      } catch (InvocationTargetException failure) {
        Throwable cause = failure.getCause();
        if (cause instanceof Exception) throw (Exception) cause;
        if (cause instanceof Error) throw (Error) cause;
        throw new IllegalStateException("Groovy execution failed", cause);
      }
    }
  }
}
