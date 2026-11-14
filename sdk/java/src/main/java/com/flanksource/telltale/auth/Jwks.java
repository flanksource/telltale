package com.flanksource.telltale.auth;

import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import java.math.BigInteger;
import java.security.KeyFactory;
import java.security.PublicKey;
import java.security.spec.RSAPublicKeySpec;
import java.util.*;

/** Parses a JWKS into RSA signing keys by kid; any unusable key rejects the whole set. */
final class Jwks {
  private Jwks() {}

  static Map<String, PublicKey> parse(String json, String source) {
    JsonElement root;
    try {
      root = JsonParser.parseString(json);
    } catch (RuntimeException error) {
      throw new IllegalArgumentException(source + " is not valid JSON", error);
    }
    if (!root.isJsonObject() || !root.getAsJsonObject().has("keys")
        || !root.getAsJsonObject().get("keys").isJsonArray())
      throw new IllegalArgumentException(source + " must be an object with a keys array");
    Map<String, PublicKey> keys = new LinkedHashMap<>();
    for (JsonElement item : root.getAsJsonObject().getAsJsonArray("keys")) {
      if (!item.isJsonObject()) throw new IllegalArgumentException(source + " keys must be objects");
      JsonObject jwk = item.getAsJsonObject();
      String kid = member(jwk, "kid", source);
      if (keys.put(kid, key(jwk, kid, source)) != null)
        throw new IllegalArgumentException(source + " has duplicate kid " + kid);
    }
    return keys;
  }

  private static PublicKey key(JsonObject jwk, String kid, String source) {
    String where = source + " key " + kid;
    if (!"RSA".equals(member(jwk, "kty", where)))
      throw new IllegalArgumentException(where + " must have kty RSA");
    if (jwk.has("alg") && !"RS256".equals(member(jwk, "alg", where)))
      throw new IllegalArgumentException(where + " alg must be RS256");
    if (jwk.has("use") && !"sig".equals(member(jwk, "use", where)))
      throw new IllegalArgumentException(where + " use must be sig");
    try {
      return KeyFactory.getInstance("RSA")
          .generatePublic(
              new RSAPublicKeySpec(integer(member(jwk, "n", where)), integer(member(jwk, "e", where))));
    } catch (Exception error) {
      throw new IllegalArgumentException(where + " is not a valid RSA public key", error);
    }
  }

  private static BigInteger integer(String base64url) {
    return new BigInteger(1, Base64.getUrlDecoder().decode(base64url));
  }

  private static String member(JsonObject object, String name, String where) {
    JsonElement value = object.get(name);
    if (value == null || !value.isJsonPrimitive() || !value.getAsJsonPrimitive().isString()
        || value.getAsString().isEmpty())
      throw new IllegalArgumentException(where + " requires string " + name);
    return value.getAsString();
  }
}
