package com.flanksource.telltale.auth;

import com.flanksource.telltale.Scope;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import java.nio.charset.StandardCharsets;
import java.security.PublicKey;
import java.security.Signature;
import java.util.*;
import java.util.concurrent.TimeUnit;

/** Verifies RS256 bearer JWTs against a fixed issuer, audience and JWKS; fails closed. */
public final class JwtVerifier {
  static final long LEEWAY_SECONDS = 60;
  static final long REFRESH_INTERVAL_NANOS = TimeUnit.SECONDS.toNanos(30);

  private final String issuer;
  private final String audience;
  private final Map<String, PublicKey> configured;
  private final JwksSource remote;
  private Map<String, PublicKey> fetched = Collections.emptyMap();
  private long lastRefresh;
  private boolean refreshed;

  /**
   * @param jwks inline JWKS JSON ({@code {"keys":[...]}}) or null
   * @param jwksUrl http(s) URL serving a JWKS, or null; at least one key source is required
   */
  public JwtVerifier(String issuer, String audience, String jwks, String jwksUrl) {
    this.issuer = nonEmpty(issuer, "issuer");
    this.audience = nonEmpty(audience, "audience");
    if (blank(jwks) && blank(jwksUrl))
      throw new IllegalArgumentException("configure jwks, jwksFile or jwksUrl");
    configured = blank(jwks) ? Collections.emptyMap() : Jwks.parse(jwks, "inline jwks");
    remote = blank(jwksUrl) ? null : new JwksSource(jwksUrl);
  }

  /**
   * The header execution tokens travel in. The Kubernetes apiserver deletes {@code Authorization}
   * from every request it proxies, so a token through {@code pods/proxy} or {@code services/proxy}
   * must use this header; direct connections may use either.
   */
  public static final String HEADER = "X-Telltale-Authorization";

  /**
   * The bearer credential of a request: {@link #HEADER}, else {@code Authorization}. Both present
   * with different values is a 400, never a silent pick.
   */
  public static String credential(String telltale, String authorization) throws AuthException {
    boolean hasTelltale = !blank(telltale), hasAuthorization = !blank(authorization);
    if (hasTelltale && hasAuthorization && !telltale.trim().equals(authorization.trim()))
      throw new AuthException(400, HEADER + " and Authorization carry different credentials");
    return hasTelltale ? telltale : authorization;
  }

  public Principal verify(String authorization, Scope required) throws AuthException {
    Principal principal = verify(authorization);
    principal.require(required);
    return principal;
  }

  public Principal verify(String authorization) throws AuthException {
    String[] parts = bearer(authorization).split("\\.", -1);
    if (parts.length != 3) throw AuthException.unauthorized("malformed bearer token");
    JsonObject header = segment(parts[0], "header");
    checkHeader(header);
    checkSignature(key(header.get("kid").getAsString()), parts);
    return claims(segment(parts[1], "payload"));
  }

  private static String bearer(String authorization) throws AuthException {
    if (authorization == null || authorization.trim().isEmpty())
      throw AuthException.unauthorized("missing bearer token");
    if (!authorization.regionMatches(true, 0, "Bearer ", 0, 7))
      throw AuthException.unauthorized("authorization must use the Bearer scheme");
    String token = authorization.substring(7).trim();
    if (token.isEmpty()) throw AuthException.unauthorized("missing bearer token");
    return token;
  }

  private static JsonObject segment(String encoded, String name) throws AuthException {
    try {
      JsonElement value =
          JsonParser.parseString(
              new String(Base64.getUrlDecoder().decode(encoded), StandardCharsets.UTF_8));
      if (!value.isJsonObject()) throw AuthException.unauthorized("token " + name + " must be an object");
      return value.getAsJsonObject();
    } catch (RuntimeException error) {
      throw AuthException.unauthorized("malformed token " + name);
    }
  }

  private static void checkHeader(JsonObject header) throws AuthException {
    String alg = Claims.string(header, "alg");
    if (!"RS256".equals(alg))
      throw AuthException.unauthorized("token alg must be RS256, got " + (alg == null ? "none" : alg));
    if (header.has("crit")) throw AuthException.unauthorized("token crit header is not supported");
    String kid = Claims.string(header, "kid");
    if (kid == null || kid.isEmpty()) throw AuthException.unauthorized("token kid is required");
  }

  private static void checkSignature(PublicKey key, String[] parts) throws AuthException {
    boolean valid;
    try {
      Signature signature = Signature.getInstance("SHA256withRSA");
      signature.initVerify(key);
      signature.update((parts[0] + "." + parts[1]).getBytes(StandardCharsets.US_ASCII));
      valid = signature.verify(Base64.getUrlDecoder().decode(parts[2]));
    } catch (Exception error) {
      throw AuthException.unauthorized("invalid token signature");
    }
    if (!valid) throw AuthException.unauthorized("invalid token signature");
  }

  private Principal claims(JsonObject claims) throws AuthException {
    long now = System.currentTimeMillis() / 1000;
    if (!issuer.equals(Claims.string(claims, "iss")))
      throw AuthException.unauthorized("token issuer is not " + issuer);
    if (!Claims.audiences(claims).contains(audience))
      throw AuthException.unauthorized("token audience does not include " + audience);
    Double exp = Claims.number(claims, "exp");
    if (exp == null) throw AuthException.unauthorized("token exp is required");
    if (now > exp + LEEWAY_SECONDS) throw AuthException.unauthorized("token expired");
    Double nbf = Claims.number(claims, "nbf");
    if (nbf != null && now < nbf - LEEWAY_SECONDS)
      throw AuthException.unauthorized("token is not valid yet");
    Claims.number(claims, "iat");
    String subject = Claims.string(claims, "sub");
    if (subject == null || subject.trim().isEmpty())
      throw AuthException.unauthorized("token sub is required");
    String scope = Claims.string(claims, "scope");
    if (scope == null) throw AuthException.unauthorized("token scope is required");
    return new Principal(subject, new LinkedHashSet<>(Arrays.asList(scope.trim().split("\\s+"))));
  }

  private PublicKey key(String kid) throws AuthException {
    PublicKey key = configured.get(kid);
    if (key != null) return key;
    synchronized (this) {
      key = fetched.get(kid);
      if (key != null) return key;
      if (remote == null) throw AuthException.unauthorized("unknown signing key " + kid);
      long now = System.nanoTime();
      if (refreshed && now - lastRefresh < REFRESH_INTERVAL_NANOS)
        throw AuthException.unauthorized("unknown signing key " + kid + "; JWKS refreshed recently");
      refreshed = true;
      lastRefresh = now;
      try {
        fetched = remote.fetch();
      } catch (Exception error) {
        throw new AuthException(401, "refresh JWKS: " + error.getMessage(), error);
      }
      key = fetched.get(kid);
      if (key == null) throw AuthException.unauthorized("unknown signing key " + kid);
      return key;
    }
  }

  private static String nonEmpty(String value, String name) {
    if (blank(value)) throw new IllegalArgumentException(name + " is required");
    return value;
  }

  static boolean blank(String value) {
    return value == null || value.trim().isEmpty();
  }
}
