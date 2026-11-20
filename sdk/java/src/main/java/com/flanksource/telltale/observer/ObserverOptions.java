package com.flanksource.telltale.observer;

import java.util.Map;

/** Validates the same integral limits at native and RPC capture boundaries. */
public final class ObserverOptions {
  private ObserverOptions() {}
  public static int positiveInt(Map<String, Object> params, String key, int defaultValue, int maximum) {
    Object value = params.get(key);
    if (value == null) return defaultValue;
    if (!(value instanceof Number) || ((Number) value).doubleValue() != ((Number) value).intValue()
        || ((Number) value).intValue() <= 0 || ((Number) value).intValue() > maximum)
      throw new IllegalArgumentException(key + " must be an integer between 1 and " + maximum);
    return ((Number) value).intValue();
  }
}
