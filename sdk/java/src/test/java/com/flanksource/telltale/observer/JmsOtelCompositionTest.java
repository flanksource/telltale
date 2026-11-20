package com.flanksource.telltale.observer;

import static org.junit.Assert.*;
import java.io.BufferedReader;
import java.io.File;
import java.io.InputStreamReader;
import java.net.ServerSocket;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import org.junit.Test;

public class JmsOtelCompositionTest {
  @Test public void capturesJmsAfterWarmedSpringHttpAndSqlWithTheOfficialAgent() throws Exception {
    Path root = Files.createDirectories(Paths.get(".tmp").toAbsolutePath());
    Path directory = Files.createTempDirectory(root, "jms-otel-composition-");
    Path log = directory.resolve("fixture.log");
    String java = Paths.get(System.getProperty("java.home"), "bin/java").toString();
    int port;
    try (ServerSocket reservation = new ServerSocket(0)) { port = reservation.getLocalPort(); }
    Process fixture = new ProcessBuilder(java, "-javaagent:" + required("telltale.gate.otel"),
        "-Dotel.service.name=jms-composition", "-Dotel.traces.exporter=none",
        "-Dotel.metrics.exporter=none", "-Dotel.logs.exporter=none", "-Dtelltale.runtime.dir=" + directory,
        "-cp", required("telltale.gate.classpath") + File.pathSeparator + required("telltale.demo.classpath"),
        "observerfixture.JmsOtelCompositionFixture", Integer.toString(port), directory.toString())
        .redirectError(log.toFile()).start();
    ExecutorService reader = Executors.newSingleThreadExecutor();
    BufferedReader output = new BufferedReader(new InputStreamReader(fixture.getInputStream(), StandardCharsets.UTF_8));
    try {
      String pid = reader.submit(() -> {
        for (String line; (line = output.readLine()) != null;) if (line.startsWith("READY ")) return line.substring(6);
        throw new AssertionError("Fixture exited before readiness: " + read(log));
      }).get(60, TimeUnit.SECONDS);
      Path config = directory.resolve("agent.json");
      Files.write(config, ("{\"token\":\"jms-composition-fixture-token\",\"allowMutation\":false,\"discovery\":\""
          + directory.resolve("discovery.json") + "\"}").getBytes(StandardCharsets.UTF_8));
      Path attachLog = directory.resolve("attach.log");
      Process attach = new ProcessBuilder(java, "-cp", required("telltale.gate.agent"), "com.flanksource.telltale.Attach",
          pid, required("telltale.gate.agent"), config.toString(), "false").redirectErrorStream(true).redirectOutput(attachLog.toFile()).start();
      try {
        assertTrue("Attach timed out", attach.waitFor(45, TimeUnit.SECONDS));
        assertEquals(read(attachLog), 0, attach.exitValue());
      } finally { attach.destroyForcibly(); }
      fixture.getOutputStream().write("CAPTURE\n".getBytes(StandardCharsets.UTF_8));
      fixture.getOutputStream().flush();
      String result = reader.submit(() -> {
        for (String line; (line = output.readLine()) != null;) if (line.startsWith("RESULT ")) return line.substring(7);
        throw new AssertionError("Fixture failed: " + read(log));
      }).get(60, TimeUnit.SECONDS);
      assertTrue(result, result.contains("\"incoming\""));
      assertTrue(result, result.contains("\"outgoing\""));
      assertTrue(read(log), fixture.waitFor(15, TimeUnit.SECONDS));
      assertEquals(read(log), 0, fixture.exitValue());
    } finally {
      fixture.destroyForcibly();
      reader.shutdownNow();
      fixture.waitFor(5, TimeUnit.SECONDS);
      output.close();
    }
  }

  private static String required(String key) {
    String value = System.getProperty(key);
    if (value == null || value.isEmpty()) throw new IllegalStateException("Missing fixture configuration " + key);
    return value;
  }

  private static String read(Path path) throws java.io.IOException {
    return new String(Files.readAllBytes(path), StandardCharsets.UTF_8);
  }
}
