package com.flanksource.telltale.auth;

import static com.flanksource.telltale.auth.TestTokens.AUDIENCE;
import static com.flanksource.telltale.auth.TestTokens.ISSUER;
import static org.junit.Assert.*;

import com.flanksource.telltale.Scope;
import com.google.gson.JsonObject;
import com.sun.net.httpserver.HttpServer;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.BeforeClass;
import org.junit.Test;

public class JwtVerifierTest {
  private static TestTokens signer;
  private static TestTokens stranger;
  private static JwtVerifier verifier;

  @BeforeClass
  public static void keys() throws Exception {
    signer = new TestTokens("primary");
    stranger = new TestTokens("primary");
    verifier = new JwtVerifier(ISSUER, AUDIENCE, signer.jwks(), null);
  }

  @Test
  public void acceptsAValidTokenAsItsSubject() throws Exception {
    Principal principal =
        verifier.verify("Bearer " + signer.token("jvm:read jvm:eval"), Scope.EVAL);
    assertEquals("operator@example.com", principal.subject());
  }

  @Test
  public void refusesMissingTokensAndForeignAlgorithms() throws Exception {
    assertRefused(401, "missing bearer token", null);
    String claims = TestTokens.encode(TestTokens.claims("jvm:read").toString());
    assertRefused(401, "RS256", "Bearer " + TestTokens.encode("{\"alg\":\"none\"}") + "." + claims + ".");
    assertRefused(401, "RS256", "Bearer " + signer.sign(TestTokens.header("HS256"), TestTokens.claims("jvm:read")));
    assertRefused(401, "signature", "Bearer " + stranger.token("jvm:read"));
  }

  @Test
  public void refusesTokensWithWrongClaims() throws Exception {
    assertRefused(401, "audience", "Bearer " + signed("aud", "another-target"));
    assertRefused(401, "issuer", "Bearer " + signed("iss", "https://elsewhere.test"));
    JsonObject expired = TestTokens.claims("jvm:read");
    expired.addProperty("exp", System.currentTimeMillis() / 1000 - JwtVerifier.LEEWAY_SECONDS - 60);
    assertRefused(401, "expired", "Bearer " + signer.sign(TestTokens.header("RS256"), expired));
    JsonObject anonymous = TestTokens.claims("jvm:read");
    anonymous.remove("sub");
    assertRefused(401, "sub", "Bearer " + signer.sign(TestTokens.header("RS256"), anonymous));
  }

  @Test
  public void refusesAMissingScopeWithForbidden() throws Exception {
    try {
      verifier.verify("Bearer " + signer.token("jvm:read jvm:trace"), Scope.EVAL);
      fail("jvm:eval was not required");
    } catch (AuthException refused) {
      assertEquals(403, refused.status());
      assertTrue(refused.getMessage(), refused.getMessage().contains("jvm:eval"));
    }
  }

  @Test
  public void refreshesTheJwksUrlOnceForAnUnknownKid() throws Exception {
    TestTokens rotated = new TestTokens("rotated");
    TestTokens unknown = new TestTokens("unknown");
    AtomicInteger fetches = new AtomicInteger();
    HttpServer server = HttpServer.create(new InetSocketAddress(InetAddress.getByName("127.0.0.1"), 0), 4);
    server.createContext(
        "/jwks",
        exchange -> {
          fetches.incrementAndGet();
          byte[] body = rotated.jwks().getBytes(StandardCharsets.UTF_8);
          exchange.sendResponseHeaders(200, body.length);
          exchange.getResponseBody().write(body);
          exchange.close();
        });
    server.start();
    try {
      String url = "http://127.0.0.1:" + server.getAddress().getPort() + "/jwks";
      JwtVerifier remote = new JwtVerifier(ISSUER, AUDIENCE, signer.jwks(), url);
      remote.verify("Bearer " + signer.token("jvm:read"), Scope.READ);
      assertEquals("inline keys need no fetch", 0, fetches.get());
      remote.verify("Bearer " + rotated.token("jvm:read"), Scope.READ);
      assertEquals(1, fetches.get());
      try {
        remote.verify("Bearer " + unknown.token("jvm:read"), Scope.READ);
        fail("an unknown kid was accepted");
      } catch (AuthException refused) {
        assertEquals(401, refused.status());
      }
      assertEquals("a second refresh within 30s must not fetch", 1, fetches.get());
    } finally {
      server.stop(0);
    }
  }

  @Test
  public void prefersTheTelltaleHeaderAndRefusesAConflictingAuthorization() throws Exception {
    assertEquals("Bearer a", JwtVerifier.credential("Bearer a", null));
    assertEquals("Bearer b", JwtVerifier.credential(null, "Bearer b"));
    assertEquals("Bearer a", JwtVerifier.credential("Bearer a", "Bearer a"));
    assertNull(JwtVerifier.credential(null, null));
    try {
      JwtVerifier.credential("Bearer a", "Bearer b");
      fail("conflicting credentials accepted");
    } catch (AuthException conflict) {
      assertEquals(400, conflict.status());
    }
  }

  private static String signed(String claim, String value) throws Exception {
    JsonObject claims = TestTokens.claims("jvm:read");
    claims.addProperty(claim, value);
    return signer.sign(TestTokens.header("RS256"), claims);
  }

  private static void assertRefused(int status, String reason, String authorization) {
    try {
      verifier.verify(authorization, Scope.READ);
      fail("token accepted, expected " + status + " " + reason);
    } catch (AuthException refused) {
      assertEquals(refused.getMessage(), status, refused.status());
      assertTrue(refused.getMessage(), refused.getMessage().contains(reason));
    }
  }
}
