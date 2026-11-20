package com.flanksource.telltale.auth;

import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import java.util.*;

/** Typed reads of JWT header and claim members; a member of the wrong type is a 401. */
final class Claims {
  private Claims() {}

  static String string(JsonObject object, String name) throws AuthException {
    JsonElement value = object.get(name);
    if (value == null || value.isJsonNull()) return null;
    if (!value.isJsonPrimitive() || !value.getAsJsonPrimitive().isString())
      throw AuthException.unauthorized("token " + name + " must be a string");
    return value.getAsString();
  }

  static Double number(JsonObject object, String name) throws AuthException {
    JsonElement value = object.get(name);
    if (value == null || value.isJsonNull()) return null;
    if (!value.isJsonPrimitive() || !value.getAsJsonPrimitive().isNumber())
      throw AuthException.unauthorized("token " + name + " must be numeric seconds");
    return value.getAsDouble();
  }

  static List<String> audiences(JsonObject claims) throws AuthException {
    JsonElement value = claims.get("aud");
    if (value == null || value.isJsonNull()) return Collections.emptyList();
    if (value.isJsonPrimitive() && value.getAsJsonPrimitive().isString())
      return Collections.singletonList(value.getAsString());
    if (!value.isJsonArray()) throw AuthException.unauthorized("token aud must be a string or array");
    List<String> result = new ArrayList<>();
    for (JsonElement item : value.getAsJsonArray()) {
      if (!item.isJsonPrimitive() || !item.getAsJsonPrimitive().isString())
        throw AuthException.unauthorized("token aud must contain strings");
      result.add(item.getAsString());
    }
    return result;
  }
}
