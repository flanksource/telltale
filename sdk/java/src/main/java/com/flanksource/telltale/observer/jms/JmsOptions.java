package com.flanksource.telltale.observer.jms;

import java.nio.charset.StandardCharsets;
import java.util.*;

final class JmsOptions {
  private JmsOptions() {}

  static void validate(Map<String, Object> params) {
    Object direction = params.get("direction");
    if (direction != null && !Arrays.asList("", "both", "incoming", "outgoing").contains(direction))
      throw new IllegalArgumentException("direction must be both, incoming or outgoing");
    Object destinations = params.get("destinations");
    if (destinations != null) {
      if (!(destinations instanceof Collection)) throw new IllegalArgumentException("destinations must be an array");
      for (Object destination : (Collection<?>) destinations)
        if (!(destination instanceof String) || ((String) destination).trim().isEmpty())
          throw new IllegalArgumentException("destinations must contain nonblank names");
    }
    if (params.containsKey("body") && !(params.get("body") instanceof Boolean))
      throw new IllegalArgumentException("body must be a boolean");
    int maxBodyBytes = number(params, "maxBodyBytes", 1 << 20);
    if (maxBodyBytes > 0 && !Boolean.TRUE.equals(params.get("body")))
      throw new IllegalArgumentException("maxBodyBytes requires body capture");
    number(params, "capacity", 50000);
  }

  private static int number(Map<String, Object> params, String key, int maximum) {
    Object value = params.get(key);
    if (value == null) return 0;
    if (!(value instanceof Number) || ((Number) value).doubleValue() != ((Number) value).intValue()
        || ((Number) value).intValue() < 0 || ((Number) value).intValue() > maximum)
      throw new IllegalArgumentException(key + " must be an integer between zero and " + maximum);
    return ((Number) value).intValue();
  }

  static Map<String, Object> project(Map<String, Object> params, Map<String, Object> row) {
    if (row.get("attributes") instanceof Map) {
      Map<?,?> attributes = (Map<?,?>) row.get("attributes");
      Object selected = params.get("destinations");
      if (selected instanceof Collection && !((Collection<?>)selected).isEmpty()
          && !((Collection<?>)selected).contains(attributes.get("destination"))) return null;
      return row;
    }
    Object destinations = params.get("destinations");
    if (destinations instanceof Collection && !((Collection<?>) destinations).isEmpty()
        && !((Collection<?>) destinations).contains(row.get("destination"))) return null;
    if (!Boolean.TRUE.equals(params.get("body"))) {
      row.remove("body"); row.remove("bodyTruncated"); row.remove("bodyUnsupported");
      return row;
    }
    Object body = row.get("body");
    if (!(body instanceof String)) return row;
    int requested = number(params, "maxBodyBytes", 1 << 20);
    int maximum = requested == 0 ? 65536 : requested;
    if (((String) body).getBytes(StandardCharsets.UTF_8).length > maximum) {
      row.put("body", JmsAccess.truncate((String) body, maximum));
      row.put("bodyTruncated", true);
    }
    return row;
  }
}
