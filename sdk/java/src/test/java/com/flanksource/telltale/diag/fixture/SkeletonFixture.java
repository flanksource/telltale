package com.flanksource.telltale.diag.fixture;

import java.util.List;
import java.util.Map;

/**
 * Every shape a skeleton has to reproduce to compile: a bounded type parameter, a superclass
 * without a no-argument constructor, an interface, final fields, a nested type in a parameter,
 * varargs, a method-level type parameter and a declared exception.
 */
public class SkeletonFixture<T extends Comparable<T>> extends SkeletonBase implements Runnable {

  static final String CONSTANT = "k";

  private final List<T> items;

  protected Map<String, List<T>> index;

  public SkeletonFixture(List<T> items) {
    super("fixture", items.size());
    this.items = items;
  }

  @Override
  public void run() {
    // Nothing; the skeleton only has to reproduce the declaration.
  }

  public String describe(Map.Entry<String, T> entry, int... counts) {
    return String.valueOf(entry) + counts.length;
  }

  protected <R> R convert(T value, R fallback) throws IllegalStateException {
    return fallback;
  }

  public List<T> getItems() {
    return items;
  }
}
