package com.flanksource.telltale;

import static org.junit.Assert.*;

import com.flanksource.telltale.auth.TestTokens;
import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import java.io.*;
import java.lang.management.ManagementFactory;
import java.nio.file.*;
import java.util.*;
import javax.management.ObjectName;
import javax.management.StandardMBean;
import org.apache.catalina.Context;
import org.apache.catalina.startup.Tomcat;
import org.junit.After;
import org.junit.BeforeClass;
import org.junit.Test;

public class WarTest {
  private static final String DIGEST = "sha256:config-under-test";
  private static TestTokens tokens;
  private static String read, trace, instrument, eval;
  private Tomcat tomcat;
  private Path directory;

  public interface LegacyRuntimeMBean {
    int getGeneration();
  }

  public static final class LegacyRuntime implements LegacyRuntimeMBean {
    public int getGeneration() {
      return 0;
    }
  }

  @BeforeClass
  public static void keys() throws Exception {
    tokens = new TestTokens("war");
    read = tokens.token("jvm:read");
    trace = tokens.token("jvm:trace");
    instrument = tokens.token("jvm:instrument");
    eval = tokens.token("jvm:eval");
  }

  @After
  public void stop() throws Exception {
    if (tomcat != null) {
      tomcat.stop();
      tomcat.destroy();
    }
    System.clearProperty("telltale.diag.config");
    ObjectName runtime = new ObjectName(RuntimeService.NAME);
    if (ManagementFactory.getPlatformMBeanServer().isRegistered(runtime))
      ManagementFactory.getPlatformMBeanServer().unregisterMBean(runtime);
  }

  @Test
  public void authorizesEveryRouteByScopeAndKeepsExtensionsAcrossRedeployment() throws Exception {
    Context context = deploy(config(Collections.emptyMap()));
    String base = base();
    JsonObject health = HttpReply.send(base + "/health", "GET", null, null).json();
    assertEquals(1, health.get("apiVersion").getAsInt());
    assertEquals(DIGEST, health.get("configDigest").getAsString());
    HttpReply anonymous = HttpReply.send(base + "/catalog", "GET", null, null);
    assertEquals(401, anonymous.status);
    assertTrue(anonymous.body, anonymous.json().get("error").getAsString().contains("bearer"));
    HttpReply catalog = HttpReply.send(base + "/catalog", "GET", null, read);
    assertEquals(catalog.body, 200, catalog.status);
    assertEquals("jvm:eval", scopeOf(catalog.body, "jvm.groovy.eval"));
    assertEquals("jvm:read", scopeOf(catalog.body, "jvm.jmx.search"));
    assertHelloListsProbeScopes(base);
    installExtension(base);
    assertScriptNeedsEvalScope(base);
    assertNotificationCaptureLifecycle(base);
    context.reload();
    HttpReply reloaded = HttpReply.send(base + "/catalog", "GET", null, read);
    assertEquals(reloaded.body, 200, reloaded.status);
    assertEquals("jvm:read", scopeOf(reloaded.body, "extension.example.greet"));
    assertEquals("[]", call(base, "{\"operation\":\"jvm.notifications.list\",\"params\":{}}", read).body);
    HttpReply jmx =
        call(
            base,
            "{\"operation\":\"jvm.jmx.search\",\"params\":{\"pattern\":\"java.lang:type=Runtime\"}}",
            read);
    assertEquals(
        Collections.singletonList("java.lang:type=Runtime"), Agent.JSON.fromJson(jmx.body, List.class));
  }

  @Test
  public void refusesTheRemovedTokenFile() throws Exception {
    deploy(config(Collections.singletonMap("tokenFile", "/var/lib/telltale/token")));
    HttpReply health = HttpReply.send(base() + "/health", "GET", null, null);
    assertEquals(500, health.status);
    assertTrue(health.body, health.body.contains("tokenFile is no longer supported"));
  }

  @Test
  public void reportsAnOutdatedRuntimeWithoutFailingDeployment() throws Exception {
    ManagementFactory.getPlatformMBeanServer()
        .registerMBean(
            new StandardMBean(new LegacyRuntime(), LegacyRuntimeMBean.class),
            new ObjectName(RuntimeService.NAME));
    deploy(config(Collections.emptyMap()));
    HttpReply health = HttpReply.send(base() + "/health", "GET", null, null);
    assertEquals(health.body, 503, health.status);
    JsonObject body = health.json();
    assertEquals("outdated", body.get("status").getAsString());
    assertEquals(0, body.get("apiVersion").getAsInt());
    assertEquals(1, body.get("required").getAsInt());
    assertTrue(body.get("error").getAsString().contains("restart the JVM"));
    HttpReply catalog = HttpReply.send(base() + "/catalog", "GET", null, read);
    assertEquals(503, catalog.status);
    assertEquals(body.get("error"), catalog.json().get("error"));
  }

  private void assertHelloListsProbeScopes(String base) throws Exception {
    HttpReply hello = HttpReply.send(base + "/hello", "GET", null, read);
    assertEquals(hello.body, 200, hello.status);
    Map<String, String> scopes = new LinkedHashMap<>();
    for (JsonElement probe : hello.json().getAsJsonArray("probes"))
      scopes.put(
          probe.getAsJsonObject().get("name").getAsString(),
          probe.getAsJsonObject().get("scope").getAsString());
    Map<String, String> expected = new LinkedHashMap<>();
    expected.put("jvm_jfr", "jvm:trace");
    expected.put("jvm_trace", "jvm:trace");
    expected.put("jvm_redefine", "jvm:instrument");
    expected.put("jvm_notifications", "jvm:trace");
    expected.put("jvm_ognl", "jvm:eval");
    assertEquals(expected, scopes);
  }

  private void installExtension(String base) throws Exception {
    String jar =
        Base64.getEncoder()
            .encodeToString(Files.readAllBytes(Paths.get("build/libs/telltale-example-extension.jar")));
    String body =
        "{\"operation\":\"jvm.extensions.install\",\"allowMutation\":true,\"params\":{\"jar\":\""
            + jar
            + "\"}}";
    assertEquals(403, call(base, body, eval).status);
    HttpReply install = call(base, body, instrument);
    assertEquals(install.body, 200, install.status);
  }

  private void assertScriptNeedsEvalScope(String base) throws Exception {
    String body =
        "{\"operation\":\"jvm.groovy.eval\",\"allowMutation\":true,\"params\":{\"source\":\"println"
            + " 'war'; return args.value * 2\",\"args\":{\"value\":7}}}";
    HttpReply forbidden = call(base, body, read);
    assertEquals(forbidden.body, 403, forbidden.status);
    assertTrue(forbidden.body, forbidden.body.contains("jvm:eval"));
    HttpReply script = call(base, body, eval);
    assertEquals(script.body, 200, script.status);
    assertTrue(script.body, script.body.contains("\"value\":14"));
  }

  private void assertNotificationCaptureLifecycle(String base) throws Exception {
    String body =
        "{\"kind\":\"jvm_notifications\",\"params\":{\"mbean\":\"java.lang:type=GarbageCollector,*\"}}";
    assertEquals(403, HttpReply.send(base + "/captures", "POST", body, read).status);
    HttpReply capture = HttpReply.send(base + "/captures", "POST", body, trace);
    assertEquals(capture.body, 200, capture.status);
    String url = base + "/captures/" + capture.json().get("id").getAsString();
    assertEquals(403, HttpReply.send(url, "POST", "", read).status);
    HttpReply renew = HttpReply.send(url + "/renew", "POST", "", trace);
    assertEquals(renew.body, 204, renew.status);
    System.gc();
    boolean decoded = false;
    long deadline = System.currentTimeMillis() + 5000;
    while (!decoded && System.currentTimeMillis() < deadline) {
      HttpReply events = HttpReply.send(url, "POST", "", trace);
      assertEquals(events.body, 200, events.status);
      assertEquals("false", events.header("X-Telltale-Complete"));
      decoded = events.body.contains("\"gc\"");
      if (!decoded) Thread.sleep(50);
    }
    assertTrue("no decoded GC collection received through the WAR capture", decoded);
    HttpReply finished = HttpReply.send(url, "DELETE", null, trace);
    assertEquals(finished.body, 200, finished.status);
    assertEquals(404, HttpReply.send(url, "POST", "", trace).status);
  }

  private static HttpReply call(String base, String body, String token) throws Exception {
    return HttpReply.send(base + "/call", "POST", body, token);
  }

  private static String scopeOf(String catalog, String operation) {
    for (JsonElement entry : Agent.JSON.fromJson(catalog, JsonArray.class))
      if (entry.getAsJsonObject().get("name").getAsString().equals(operation))
        return entry.getAsJsonObject().get("scope").getAsString();
    throw new AssertionError(operation + " missing from catalog " + catalog);
  }

  private Path config(Map<String, String> extra) throws Exception {
    directory =
        Files.createTempDirectory(Files.createDirectories(Paths.get(".tmp").toAbsolutePath()), "war-");
    Properties config = new Properties();
    config.setProperty("issuer", TestTokens.ISSUER);
    config.setProperty("audience", TestTokens.AUDIENCE);
    config.setProperty("jwks", tokens.jwks());
    config.setProperty("configDigest", DIGEST);
    config.setProperty("runtimeDir", directory.resolve("runtime").toString());
    config.setProperty("extensionsDir", directory.resolve("extensions").toString());
    config.setProperty("allowMutation", "true");
    for (Map.Entry<String, String> entry : extra.entrySet())
      config.setProperty(entry.getKey(), entry.getValue());
    Path file = directory.resolve("diagnostics.properties");
    try (OutputStream output = Files.newOutputStream(file)) {
      config.store(output, "test");
    }
    return file;
  }

  private Context deploy(Path configFile) throws Exception {
    Files.createDirectories(directory.resolve("tomcat/webapps"));
    tomcat = new Tomcat();
    tomcat.setAddDefaultWebXmlToWebapp(false);
    tomcat.setBaseDir(directory.resolve("tomcat").toString());
    tomcat.setPort(0);
    tomcat.getConnector();
    Context context =
        tomcat.addWebapp(
            "/diag", Paths.get("build/libs/telltale-diag.war").toAbsolutePath().toString());
    // A context parameter would not survive context.reload(); the system property does.
    System.setProperty("telltale.diag.config", configFile.toString());
    tomcat.start();
    return context;
  }

  private String base() {
    return "http://127.0.0.1:" + tomcat.getConnector().getLocalPort() + "/diag";
  }
}
