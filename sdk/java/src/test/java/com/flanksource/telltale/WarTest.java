package com.flanksource.telltale;

import static org.junit.Assert.*;

import java.io.*;
import java.lang.management.ManagementFactory;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import javax.management.ObjectName;
import org.apache.catalina.Context;
import org.apache.catalina.startup.Tomcat;
import org.junit.Test;

public class WarTest {
  private static final String TOKEN = "example-diagnostic-test-token";

  @Test
  public void bootstrapsAnIsolatedRuntimeAndKeepsExtensionsAcrossRedeployment() throws Exception {
    Path directory =
        Files.createTempDirectory(
            Files.createDirectories(Paths.get(".tmp").toAbsolutePath()), "war-");
    Files.write(directory.resolve("token"), TOKEN.getBytes(StandardCharsets.UTF_8));
    Properties config = new Properties();
    config.setProperty("tokenFile", directory.resolve("token").toString());
    config.setProperty("runtimeDir", directory.resolve("runtime").toString());
    config.setProperty("extensionsDir", directory.resolve("extensions").toString());
    config.setProperty("allowMutation", "true");
    try (OutputStream output = Files.newOutputStream(directory.resolve("diagnostics.properties"))) {
      config.store(output, "test");
    }
    String previous = System.getProperty("telltale.diag.config");
    System.setProperty(
        "telltale.diag.config", directory.resolve("diagnostics.properties").toString());
    Files.createDirectories(directory.resolve("tomcat/webapps"));
    Tomcat tomcat = new Tomcat();
    tomcat.setAddDefaultWebXmlToWebapp(false);
    tomcat.setBaseDir(directory.resolve("tomcat").toString());
    tomcat.setPort(0);
    tomcat.getConnector();
    Context context =
        tomcat.addWebapp(
            "/diag", Paths.get("build/libs/telltale-diag.war").toAbsolutePath().toString());
    context.addParameter("configFile", directory.resolve("diagnostics.properties").toString());
    try {
      tomcat.start();
      String base = "http://127.0.0.1:" + tomcat.getConnector().getLocalPort() + "/diag";
      assertEquals(401, request(base, "/catalog", null, null).status);
      Reply catalog = request(base, "/catalog", null, TOKEN);
      assertEquals(catalog.body, 200, catalog.status);
      assertTrue(catalog.body.contains("jvm.groovy.eval"));
      String jar =
          Base64.getEncoder()
              .encodeToString(
                  Files.readAllBytes(Paths.get("build/libs/telltale-example-extension.jar")));
      Reply install =
          request(
              base,
              "/call",
              "{\"operation\":\"jvm.extensions.install\",\"allowMutation\":true,\"params\":{\"jar\":\""
                  + jar
                  + "\"}}",
              TOKEN);
      assertEquals(install.body, 200, install.status);
      Reply script =
          request(
              base,
              "/call",
              "{\"operation\":\"jvm.groovy.eval\",\"allowMutation\":true,\"params\":{\"source\":\"println"
                  + " 'war'; return args.value * 2\",\"args\":{\"value\":7}}}",
              TOKEN);
      assertEquals(script.body, 200, script.status);
      assertTrue(script.body, script.body.contains("\"value\":14"));
      Reply capture =
          request(
              base,
              "/captures",
              "{\"kind\":\"jvm_notifications\",\"params\":{\"mbean\":\"java.lang:type=GarbageCollector,*\"}}",
              TOKEN);
      assertEquals(capture.body, 200, capture.status);
      String captureId =
          Agent.JSON
              .fromJson(capture.body, com.google.gson.JsonObject.class)
              .get("id")
              .getAsString();
      System.gc();
      boolean decoded = false;
      long deadline = System.currentTimeMillis() + 5000;
      while (System.currentTimeMillis() < deadline) {
        Reply events = request(base, "/captures/" + captureId, "", TOKEN);
        assertEquals(events.body, 200, events.status);
        if (events.body.contains("\"gc\"")) {
          decoded = true;
          break;
        }
        Thread.sleep(50);
      }
      assertTrue("no decoded GC collection received through the WAR capture", decoded);
      context.reload();
      Reply reloaded = request(base, "/catalog", null, TOKEN);
      assertEquals(reloaded.body, 200, reloaded.status);
      assertTrue(reloaded.body.contains("extension.example.greet"));
      Reply notifications =
          request(base, "/call", "{\"operation\":\"jvm.notifications.list\",\"params\":{}}", TOKEN);
      assertEquals(notifications.body, 200, notifications.status);
      assertEquals("[]", notifications.body);
      Reply jmx =
          request(
              base,
              "/call",
              "{\"operation\":\"jvm.jmx.search\",\"params\":{\"pattern\":\"java.lang:type=Runtime\"}}",
              TOKEN);
      assertEquals(jmx.body, 200, jmx.status);
      assertEquals(
          Collections.singletonList("java.lang:type=Runtime"),
          Agent.JSON.fromJson(jmx.body, List.class));
    } finally {
      tomcat.stop();
      tomcat.destroy();
      if (previous == null) System.clearProperty("telltale.diag.config");
      else System.setProperty("telltale.diag.config", previous);
      ObjectName runtime = new ObjectName(RuntimeService.NAME);
      if (ManagementFactory.getPlatformMBeanServer().isRegistered(runtime))
        ManagementFactory.getPlatformMBeanServer().unregisterMBean(runtime);
    }
  }

  private static final class Reply {
    final int status;
    final String body;

    Reply(int status, String body) {
      this.status = status;
      this.body = body;
    }
  }

  private static Reply request(String base, String path, String body, String token)
      throws Exception {
    HttpURLConnection connection = (HttpURLConnection) new URL(base + path).openConnection();
    connection.setConnectTimeout(5000);
    connection.setReadTimeout(30000);
    if (token != null) connection.setRequestProperty("Authorization", "Bearer " + token);
    if (body != null) {
      connection.setRequestMethod("POST");
      connection.setDoOutput(true);
      connection.setRequestProperty("Content-Type", "application/json");
      try (OutputStream output = connection.getOutputStream()) {
        output.write(body.getBytes(StandardCharsets.UTF_8));
      }
    }
    int status = connection.getResponseCode();
    ByteArrayOutputStream bytes = new ByteArrayOutputStream();
    try (InputStream input =
        status >= 400 ? connection.getErrorStream() : connection.getInputStream()) {
      byte[] buffer = new byte[4096];
      int count;
      while ((count = input.read(buffer)) != -1) bytes.write(buffer, 0, count);
    } finally {
      connection.disconnect();
    }
    return new Reply(status, new String(bytes.toByteArray(), StandardCharsets.UTF_8));
  }
}
