package com.flanksource.telltale.auth;

import com.google.gson.JsonArray;
import com.google.gson.JsonObject;
import java.math.BigInteger;
import java.nio.charset.StandardCharsets;
import java.security.*;
import java.security.interfaces.RSAPublicKey;
import java.util.Base64;

/** An in-test RSA signing key that issues RS256 JWTs and publishes itself as a JWKS. */
public final class TestTokens {
  public static final String ISSUER = "https://telltale.test";
  public static final String AUDIENCE = "target-under-test";
  public final String kid;
  private final KeyPair keys;

  public TestTokens(String kid) throws Exception {
    this.kid = kid;
    KeyPairGenerator generator = KeyPairGenerator.getInstance("RSA");
    generator.initialize(2048);
    keys = generator.generateKeyPair();
  }

  public JsonObject jwk() {
    RSAPublicKey key = (RSAPublicKey) keys.getPublic();
    JsonObject jwk = new JsonObject();
    jwk.addProperty("kty", "RSA");
    jwk.addProperty("kid", kid);
    jwk.addProperty("alg", "RS256");
    jwk.addProperty("use", "sig");
    jwk.addProperty("n", unsigned(key.getModulus()));
    jwk.addProperty("e", unsigned(key.getPublicExponent()));
    return jwk;
  }

  public String jwks() {
    JsonArray list = new JsonArray();
    list.add(jwk());
    JsonObject jwks = new JsonObject();
    jwks.add("keys", list);
    return jwks.toString();
  }

  /** Claims valid for {@link #ISSUER}/{@link #AUDIENCE} for five minutes. */
  public static JsonObject claims(String scope) {
    long now = System.currentTimeMillis() / 1000;
    JsonObject claims = new JsonObject();
    claims.addProperty("iss", ISSUER);
    claims.addProperty("aud", AUDIENCE);
    claims.addProperty("sub", "operator@example.com");
    claims.addProperty("iat", now);
    claims.addProperty("exp", now + 300);
    claims.addProperty("scope", scope);
    return claims;
  }

  public String token(String scope) throws Exception {
    return sign(header("RS256"), claims(scope));
  }

  public static JsonObject header(String alg) {
    JsonObject header = new JsonObject();
    header.addProperty("alg", alg);
    header.addProperty("typ", "JWT");
    return header;
  }

  public String sign(JsonObject header, JsonObject claims) throws Exception {
    if (!header.has("kid")) header.addProperty("kid", kid);
    String input = encode(header.toString()) + "." + encode(claims.toString());
    Signature signature = Signature.getInstance("SHA256withRSA");
    signature.initSign(keys.getPrivate());
    signature.update(input.getBytes(StandardCharsets.US_ASCII));
    return input + "." + Base64.getUrlEncoder().withoutPadding().encodeToString(signature.sign());
  }

  public static String encode(String json) {
    return Base64.getUrlEncoder()
        .withoutPadding()
        .encodeToString(json.getBytes(StandardCharsets.UTF_8));
  }

  private static String unsigned(BigInteger value) {
    byte[] bytes = value.toByteArray();
    if (bytes[0] == 0 && bytes.length > 1) bytes = java.util.Arrays.copyOfRange(bytes, 1, bytes.length);
    return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes);
  }
}
