package com.flanksource.telltale;

import static org.junit.Assert.*;

import com.flanksource.telltale.auth.TestTokens;
import com.flanksource.telltale.observer.ObserverRuntime;
import java.lang.management.ManagementFactory;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import javax.management.ObjectName;
import org.junit.Test;

public class HttpListenerTest {
  @Test
  public void servesTheWarRoutesBehindJwtAuthorization() throws Exception {
    TestTokens tokens = new TestTokens("listener");
    Path directory =
        Files.createTempDirectory(
            Files.createDirectories(Paths.get(".tmp").toAbsolutePath()), "listener-");
    Map<String, Object> config = new LinkedHashMap<>();
    config.put("bind", "127.0.0.1");
    config.put("port", 0);
    config.put("portFile", directory.resolve("port").toString());
    config.put("issuer", TestTokens.ISSUER);
    config.put("audience", TestTokens.AUDIENCE);
    config.put("jwks", Agent.JSON.fromJson(tokens.jwks(), Map.class));
    config.put("configDigest", "sha256:listener");
    Map<String, Object> runtimeConfig = new LinkedHashMap<>();
    runtimeConfig.put("service", "listener-fixture");
    runtimeConfig.put("resource", Collections.singletonMap("deployment.environment.name", "integration"));
    RuntimeService.acquire(runtimeConfig);
    try (HttpListener listener = HttpListener.start(config)) {
      String port =
          new String(Files.readAllBytes(directory.resolve("port")), StandardCharsets.UTF_8);
      assertEquals(Integer.toString(listener.port()), port);
      String base = "http://127.0.0.1:" + port;
      HttpReply health = HttpReply.send(base + "/health", "GET", null, null);
      assertEquals(health.body, 200, health.status);
      assertEquals("sha256:listener", health.json().get("configDigest").getAsString());
      HttpReply hello = HttpReply.send(base + "/hello", "GET", null, tokens.token("jvm:read"));
      assertEquals(hello.body, 200, hello.status);
      Map<String, Object> expectedResource = new LinkedHashMap<>();
      expectedResource.put("service.name", "listener-fixture");
      expectedResource.put("process.pid", ManagementFactory.getRuntimeMXBean().getName().split("@")[0]);
      expectedResource.put("deployment.environment.name", "integration");
      expectedResource.put("telltale.source.instance", ObserverRuntime.SOURCE);
      assertEquals(Agent.JSON.toJsonTree(expectedResource), hello.json().get("resource"));
      String search =
          "{\"operation\":\"jvm.jmx.search\",\"params\":{\"pattern\":\"java.lang:type=Runtime\"}}";
      HttpReply anonymous = HttpReply.send(base + "/call", "POST", search, null);
      assertEquals(anonymous.body, 401, anonymous.status);
      HttpReply authorized = HttpReply.send(base + "/call", "POST", search, tokens.token("jvm:read"));
      assertEquals(authorized.body, 200, authorized.status);
      assertEquals(
          Collections.singletonList("java.lang:type=Runtime"),
          Agent.JSON.fromJson(authorized.body, List.class));
      String read = "Bearer " + tokens.token("jvm:read");
      Map<String, String> telltale = Collections.singletonMap("X-Telltale-Authorization", read);
      HttpReply viaHeader = HttpReply.sendHeaders(base + "/call", "POST", search, telltale);
      assertEquals("the apiserver-safe header alone authorizes", 200, viaHeader.status);
      Map<String, String> both = new LinkedHashMap<>();
      both.put("X-Telltale-Authorization", read);
      both.put("Authorization", read);
      assertEquals(200, HttpReply.sendHeaders(base + "/call", "POST", search, both).status);
      both.put("Authorization", "Bearer " + tokens.token("jvm:trace"));
      HttpReply conflict = HttpReply.sendHeaders(base + "/call", "POST", search, both);
      assertEquals(conflict.body, 400, conflict.status);
      assertTrue(conflict.body, conflict.body.contains("different credentials"));
    } finally {
      ManagementFactory.getPlatformMBeanServer().unregisterMBean(new ObjectName(RuntimeService.NAME));
    }
  }
}
