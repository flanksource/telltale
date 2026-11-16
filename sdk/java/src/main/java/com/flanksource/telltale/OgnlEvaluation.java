package com.flanksource.telltale;

import java.lang.reflect.*;
import java.util.Map;
import ognl.*;

final class OgnlEvaluation {
  static Object evaluate(Map<String, Object> params, ClassLoader loader) throws Exception {
    ClassResolver resolver =
        new ClassResolver() {
          public Class<?> classForName(String name, Map context) throws ClassNotFoundException {
            return Class.forName(name, true, loader);
          }
        };
    MemberAccess access =
        new MemberAccess() {
          public boolean isAccessible(Map context, Object target, Member member, String property) {
            return true;
          }

          public Object setup(Map context, Object target, Member member, String property) {
            if (member instanceof AccessibleObject && !((AccessibleObject) member).isAccessible()) {
              ((AccessibleObject) member).setAccessible(true);
              return Boolean.FALSE;
            }
            return null;
          }

          public void restore(
              Map context, Object target, Member member, String property, Object state) {
            if (state != null) ((AccessibleObject) member).setAccessible((Boolean) state);
          }
        };
    OgnlContext context = new OgnlContext(resolver, null, access);
    ClassLoader previous = Thread.currentThread().getContextClassLoader();
    try {
      Thread.currentThread().setContextClassLoader(loader);
      Object value =
          Ognl.getValue(
              Ognl.parseExpression(Diagnostics.required(params, "expression")),
              context,
              context.getRoot());
      if (value == null
          || value instanceof String
          || value instanceof Number
          || value instanceof Boolean) return java.util.Collections.singletonMap("value", value);
      return java.util.Collections.singletonMap(
          "graph",
          new com.flanksource.telltale.diag.graph.ValueGraph(
                  com.flanksource.telltale.diag.graph.GraphLimits.probe(
                      4, 100, 1000, 4096, 1048576))
              .root(value, "result", null));
    } finally {
      Thread.currentThread().setContextClassLoader(previous);
    }
  }
}
