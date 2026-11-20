package com.flanksource.telltale.observer;

import static org.junit.Assert.*;
import com.google.gson.*;
import io.opentelemetry.proto.collector.trace.v1.ExportTraceServiceRequest;
import io.opentelemetry.proto.common.v1.KeyValue;
import io.opentelemetry.proto.common.v1.AnyValue;
import io.opentelemetry.proto.trace.v1.Span;
import io.opentelemetry.proto.profiles.v1development.ProfilesData;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.net.ServerSocket;
import java.util.*;
import java.util.concurrent.*;
import java.util.regex.*;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.junit.runners.Parameterized;

@RunWith(Parameterized.class)
public class NoRestartOtelTest {
  private final String runtimeVersion;
  public NoRestartOtelTest(String runtimeVersion) { this.runtimeVersion = runtimeVersion; }
  @Parameterized.Parameters(name = "runtime={0}")
  public static Collection<Object[]> runtimes() throws Exception {
    String executable = System.getProperty("telltale.gate.fixtureJava", Paths.get(System.getProperty("java.home"), "bin", "java").toString());
    Process version = new ProcessBuilder(executable, "-version").redirectErrorStream(true).start();
    try {
      if (!version.waitFor(15, TimeUnit.SECONDS)) throw new AssertionError("fixture JVM version command timed out: " + executable);
      String output;
      try (BufferedReader reader = new BufferedReader(new InputStreamReader(version.getInputStream(), StandardCharsets.UTF_8))) {
        StringBuilder lines = new StringBuilder();
        for (String line; (line = reader.readLine()) != null;) lines.append(line).append('\n');
        output = lines.toString();
      }
      assertEquals(output, 0, version.exitValue());
      Matcher found = Pattern.compile("(?:java|openjdk) version \"([^\"]+)\"").matcher(output);
      if (!found.find()) throw new AssertionError("unrecognized fixture JVM version: " + output);
      return Collections.singletonList(new Object[] {found.group(1)});
    } finally { version.destroyForcibly(); }
  }
  @Test public void observesExistingSampledProviderWithoutRestart() throws Exception { verify(true, runtimeVersion); }
  @Test public void observesExistingUnsampledInstrumenterWithoutRestart() throws Exception { verify(false, runtimeVersion); }

  private static void verify(boolean sampled, String runtimeVersion) throws Exception {
    Path root = Paths.get(".tmp").toAbsolutePath();
    Files.createDirectories(root);
    Path directory = Files.createTempDirectory(root, "no-restart-otel-");
    Path error = directory.resolve("fixture.log");
    String java = Paths.get(System.getProperty("java.home"), "bin", "java").toString();
    String fixtureJava = System.getProperty("telltale.gate.fixtureJava", java);
    String agent = required("telltale.gate.agent");
    int port;
    try (ServerSocket reservation = new ServerSocket(0)) { port = reservation.getLocalPort(); }
    Process fixture = new ProcessBuilder(fixtureJava, "-javaagent:" + required("telltale.gate.otel"),
        "-Dotel.traces.exporter=otlp", "-Dotel.metrics.exporter=none", "-Dotel.logs.exporter=none",
        "-Dotel.exporter.otlp.traces.protocol=http/protobuf",
        "-Dotel.exporter.otlp.traces.endpoint=http://127.0.0.1:" + port + "/official/v1/traces",
        "-Dotel.bsp.schedule.delay=100", "-Dotel.bsp.max.export.batch.size=1",
        "-Dotel.traces.sampler=" + (sampled ? "always_on" : "always_off"),
        "-Dotel.instrumentation.java-http-server.enabled=false",
        "-Dotel.instrumentation.okhttp.enabled=false",
        "-Dotel.service.name=no-restart-gate", "-Dtelltale.runtime.dir=" + directory,
        "-cp", fixtureClasspath(), "observerfixture.NoRestartOtelFixture",
        Boolean.toString(sampled), Integer.toString(port)).redirectError(error.toFile()).start();
    ExecutorService reader = Executors.newSingleThreadExecutor();
    BufferedReader output = new BufferedReader(new InputStreamReader(fixture.getInputStream(), StandardCharsets.UTF_8));
    try {
      JsonObject before = response(reader, output, "READY ", error);
      assertEquals(runtimeVersion, before.get("javaVersion").getAsString());
      assertTrue("Telltale must be absent before attach", before.get("telltaleAbsent").getAsBoolean());
      assertEquals(sampled ? 1 : 0, before.get("before").getAsInt());
      assertEquals(sampled ? 2 : 0, before.get("officialBefore").getAsInt());
      Path config = directory.resolve("agent.json");
      Files.write(config, ("{\"token\":\"no-restart-gate-token\",\"allowMutation\":false,\"discovery\":\""
          + directory.resolve("discovery.json") + "\"}").getBytes(StandardCharsets.UTF_8));
      String attachClasspath = agent;
      if (before.get("javaVersion").getAsString().startsWith("1.8")) {
        Path tools = Paths.get(fixtureJava).toAbsolutePath().getParent().getParent().resolve("lib/tools.jar");
        assertTrue("Java 8 attach requires its JDK tools.jar: " + tools, Files.isRegularFile(tools));
        attachClasspath += File.pathSeparator + tools;
      }
      Process attach = new ProcessBuilder(fixtureJava, "-cp", attachClasspath, "com.flanksource.telltale.Attach",
          before.get("pid").getAsString(), agent, config.toString(), "false")
          .redirectErrorStream(true).redirectOutput(directory.resolve("attach.log").toFile()).start();
      try {
        assertTrue("attach timed out", attach.waitFor(45, TimeUnit.SECONDS));
        assertEquals(read(directory.resolve("attach.log")), 0, attach.exitValue());
      } finally { attach.destroyForcibly(); }
      fixture.getOutputStream().write("CAPTURE\n".getBytes(StandardCharsets.UTF_8));
      fixture.getOutputStream().flush();
      JsonObject result = response(reader, output, "RESULT ", error);
      assertEquals(runtimeVersion, result.get("javaVersion").getAsString());
      Files.write(directory.resolve("result.json"), result.toString().getBytes(StandardCharsets.UTF_8));
      String expectedJava = System.getProperty("telltale.gate.expectedJava");
      if (expectedJava != null) assertTrue(result.toString(), result.get("javaVersion").getAsString().startsWith(expectedJava));
      assertEquals(before.get("pid"), result.get("pid"));
      assertEquals(sampled ? 2 : 0, result.get("during").getAsInt());
      assertEquals(sampled ? 3 : 0, result.get("after").getAsInt());
      assertEquals(sampled ? 4 : 0, result.get("officialDuring").getAsInt());
      assertEquals(sampled ? 6 : 0, result.get("officialAfter").getAsInt());
      assertEquals(0, result.get("afterRows").getAsInt());
      assertProductionHttp(result);
      assertProductionSql(result);
      JsonArray operations = result.getAsJsonArray("operations");
      assertEquals(result.toString(), 2, operations.size());
      assertEquals("start", operations.get(0).getAsJsonObject().get("phase").getAsString());
      assertEquals("end", operations.get(1).getAsJsonObject().get("phase").getAsString());
      assertEquals("GET /instrumenter/during", operations.get(0).getAsJsonObject().get("name").getAsString());
      JsonArray officialOperations = result.getAsJsonArray("officialOperations");
      assertEquals(result.toString(), 2, officialOperations.size());
      assertTrue(officialOperations.get(0).getAsJsonObject().get("instrumentationName").getAsString().contains("http-url-connection"));
      assertEquals("start", officialOperations.get(0).getAsJsonObject().get("phase").getAsString());
      assertEquals("end", officialOperations.get(1).getAsJsonObject().get("phase").getAsString());
      assertEquals(officialOperations.get(0).getAsJsonObject().get("operationId"), officialOperations.get(1).getAsJsonObject().get("operationId"));
      assertEquals(sampled, officialOperations.get(0).getAsJsonObject().get("sampled").getAsBoolean());
      long sentinelSpans = 0;
      for (JsonElement row : result.getAsJsonArray("spans")) {
        String name = row.getAsJsonObject().get("operation").getAsString();
        if (name.equals("GET") || name.equals("GET /gate/during") || name.equals("GET /instrumenter/during")) sentinelSpans++;
      }
      assertEquals(sampled ? 3L : 0L, sentinelSpans);
      if (sampled) {
        JsonObject span = findMirror(result.getAsJsonArray("spans"), "GET /gate/during");
        assertEquals("GET /gate/during", span.get("operation").getAsString());
        assertFalse(span.get("native").getAsString().isEmpty());
        Span wire = ExportTraceServiceRequest.parseFrom(Base64.getDecoder().decode(result.get("wireDuring").getAsString()))
            .getResourceSpans(0).getScopeSpans(0).getSpans(0);
        Span mirror = ExportTraceServiceRequest.parseFrom(Base64.getDecoder().decode(span.get("native").getAsString()))
            .getResourceSpans(0).getScopeSpans(0).getSpans(0);
        assertEquals("mirror must preserve the original OTLP span", canonical(wire), canonical(mirror));
        assertEquals(9007199254740993L, wire.getAttributesList().stream()
            .filter(attribute -> attribute.getKey().equals("gate.precise")).findFirst().get().getValue().getIntValue());
        assertEquals(9007199254740993L, wire.getEvents(0).getAttributes(0).getValue().getIntValue());
        Span officialWire = ExportTraceServiceRequest.parseFrom(Base64.getDecoder().decode(result.get("officialWireDuring").getAsString()))
            .getResourceSpans(0).getScopeSpans(0).getSpans(0);
        JsonObject officialSpan = findMirror(result.getAsJsonArray("spans"), officialWire.getName());
        assertEquals(officialOperations.get(0).getAsJsonObject().get("traceId"), officialSpan.get("traceId"));
        Span officialMirror = ExportTraceServiceRequest.parseFrom(Base64.getDecoder().decode(officialSpan.get("native").getAsString()))
            .getResourceSpans(0).getScopeSpans(0).getSpans(0);
        assertEquals("official agent original OTLP delivery must match mirror", canonical(officialWire), canonical(officialMirror));
        assertEquals(JsonParser.parseString("[\"GET /gate/before\",\"GET /gate/during\",\"GET /gate/after\"]"), result.get("exportedNames"));
      }
      assertTrue(read(error), fixture.waitFor(20, TimeUnit.SECONDS));
      assertEquals(read(error), 0, fixture.exitValue());
      assertTrue("official OTel agent must initialize", read(error).contains("2.33.1"));
    } finally {
      fixture.destroyForcibly();
      reader.shutdownNow();
      fixture.waitFor(5, TimeUnit.SECONDS);
      output.close();
    }
  }

  private static JsonObject findMirror(JsonArray rows, String name) {
    for (JsonElement row : rows)
      if (name.equals(row.getAsJsonObject().get("operation").getAsString())) return row.getAsJsonObject();
    throw new AssertionError("missing mirrored " + name + ": " + rows);
  }

  private static String fixtureClasspath() {
    String classpath = required("telltale.gate.classpath");
    if (!System.getProperty("telltale.gate.expectedJava", "").startsWith("1.8")) return classpath;
    List<String> entries = new ArrayList<>();
    boolean replaced = false;
    for (String entry : classpath.split(File.pathSeparator)) {
      if (Paths.get(entry).getFileName().toString().startsWith("h2-")) {
        entries.add(required("telltale.gate.java8H2")); replaced = true;
      } else entries.add(entry);
    }
    if (!replaced) throw new IllegalStateException("Java 8 SQL fixture requires the pinned H2 driver entry");
    return String.join(File.pathSeparator, entries);
  }

  private static void assertProductionSql(JsonObject result) throws Exception {
    assertTrue(result.get("sqlReleased").getAsBoolean());
    JsonArray rows = result.getAsJsonArray("sqlRows");
    assertEquals(result.toString(), 2, rows.size());
    JsonObject reactive = null;
    for (JsonElement row : rows) if ("r2dbc".equals(row.getAsJsonObject().get("api").getAsString())) reactive = row.getAsJsonObject();
    assertNotNull("native R2DBC completion must be captured", reactive);
    JsonArray snapshots = result.getAsJsonArray("sqlProfiles");
    ProfilesData first = ProfilesData.parseFrom(Base64.getDecoder().decode(snapshots.get(0).getAsString()));
    ProfilesData second = ProfilesData.parseFrom(Base64.getDecoder().decode(snapshots.get(1).getAsString()));
    ProfilesData empty = ProfilesData.parseFrom(Base64.getDecoder().decode(snapshots.get(2).getAsString()));
    io.opentelemetry.proto.profiles.v1development.Profile before = first.getResourceProfiles(0).getScopeProfiles(0).getProfiles(0);
    io.opentelemetry.proto.profiles.v1development.Profile after = second.getResourceProfiles(0).getScopeProfiles(0).getProfiles(0);
    assertEquals(1, before.getSamplesCount()); assertEquals(1, after.getSamplesCount());
    assertTrue(after.getSamples(0).getValues(0) > before.getSamples(0).getValues(0));
    assertEquals("awaiting-consumption", profileAttribute(first, before.getSamples(0).getAttributeIndicesList(), "phase"));
    assertEquals(reactive.get("operationId").getAsString(), operationId(first));
    assertEquals(operationId(first), operationId(second));
    assertEquals(result.get("sqlSource").getAsString(), profileAttribute(first, before.getAttributeIndicesList(), "telltale.source.instance"));
    assertEmptyControl(empty);
  }

  private static void assertProductionHttp(JsonObject result) throws Exception {
    assertTrue(result.get("productionReleased").getAsBoolean());
    JsonArray rows = result.getAsJsonArray("http");
    assertEquals(result.toString(), 1, rows.size());
    JsonObject response = rows.get(0).getAsJsonObject().getAsJsonObject("response");
    assertEquals(200, response.get("status").getAsInt());
    assertEquals("gate-body", response.getAsJsonObject("content").get("text").getAsString());
    JsonArray snapshots = result.getAsJsonArray("profiles");
    assertEquals(3, snapshots.size());
    ProfilesData first = ProfilesData.parseFrom(Base64.getDecoder().decode(snapshots.get(0).getAsString()));
    ProfilesData second = ProfilesData.parseFrom(Base64.getDecoder().decode(snapshots.get(1).getAsString()));
    ProfilesData empty = ProfilesData.parseFrom(Base64.getDecoder().decode(snapshots.get(2).getAsString()));
    io.opentelemetry.proto.profiles.v1development.Profile before = first.getResourceProfiles(0).getScopeProfiles(0).getProfiles(0);
    io.opentelemetry.proto.profiles.v1development.Profile after = second.getResourceProfiles(0).getScopeProfiles(0).getProfiles(0);
    assertEquals(1, before.getSamplesCount()); assertEquals(1, after.getSamplesCount());
    assertTrue(after.getSamples(0).getValues(0) > before.getSamples(0).getValues(0));
    assertEmptyControl(empty);
    assertEquals(rows.get(0).getAsJsonObject().get("requestId").getAsString(), operationId(first));
    assertEquals(operationId(first), operationId(second));
    assertEquals(result.get("source").getAsString(), profileAttribute(first, before.getAttributeIndicesList(), "telltale.source.instance"));
  }

  private static String operationId(ProfilesData data) {
    return profileAttribute(data, data.getResourceProfiles(0).getScopeProfiles(0).getProfiles(0).getSamples(0).getAttributeIndicesList(), "telltale.operation.id");
  }

  private static void assertEmptyControl(ProfilesData data) {
    io.opentelemetry.proto.profiles.v1development.Profile profile = data.getResourceProfiles(0).getScopeProfiles(0).getProfiles(0);
    assertEquals(2L, profileValue(data, profile.getAttributeIndicesList(), "telltale.schema.version").getIntValue());
    assertEquals(0L, profileValue(data, profile.getAttributeIndicesList(), "telltale.snapshot.observed").getIntValue());
    assertTrue(profileValue(data, profile.getAttributeIndicesList(), "telltale.snapshot.complete").getBoolValue());
    assertEquals(1, profile.getSamplesCount());
    io.opentelemetry.proto.profiles.v1development.Sample sample = profile.getSamples(0);
    assertEquals(Collections.singletonList(0L), sample.getValuesList());
    assertEquals(Collections.singletonList(profile.getTimeUnixNano() + profile.getDurationNano()), sample.getTimestampsUnixNanoList());
    assertEquals(1, sample.getAttributeIndicesCount());
    assertEquals("snapshot_control", profileAttribute(data, sample.getAttributeIndicesList(), "telltale.sample.kind"));
    assertEquals(0, sample.getStackIndex());
    assertEquals(0, sample.getLinkIndex());
  }
  private static String profileAttribute(ProfilesData data, List<Integer> indices, String name) {
    return profileValue(data, indices, name).getStringValue();
  }

  private static AnyValue profileValue(ProfilesData data, List<Integer> indices, String name) {
    for (int index : indices) {
      io.opentelemetry.proto.profiles.v1development.KeyValueAndUnit attribute = data.getDictionary().getAttributeTable(index);
      if (data.getDictionary().getStringTable(attribute.getKeyStrindex()).equals(name))
        return attribute.getValue();
    }
    throw new AssertionError("profile missing " + name);
  }

  private static Span canonical(Span span) {
    Span.Builder result = span.toBuilder().clearAttributes().addAllAttributes(sorted(span.getAttributesList()));
    for (int index = 0; index < span.getEventsCount(); index++)
      result.setEvents(index, span.getEvents(index).toBuilder().clearAttributes()
          .addAllAttributes(sorted(span.getEvents(index).getAttributesList())));
    for (int index = 0; index < span.getLinksCount(); index++)
      result.setLinks(index, span.getLinks(index).toBuilder().clearAttributes()
          .addAllAttributes(sorted(span.getLinks(index).getAttributesList())));
    return result.build();
  }
  private static List<KeyValue> sorted(List<KeyValue> attributes) {
    List<KeyValue> result = new ArrayList<>(attributes);
    result.sort(Comparator.comparing(KeyValue::getKey));
    return result;
  }

  private static JsonObject response(ExecutorService reader, BufferedReader output, String prefix, Path error) throws Exception {
    Future<String> pending = reader.submit(() -> {
      for (String line; (line = output.readLine()) != null;) if (line.startsWith(prefix)) return line.substring(prefix.length());
      throw new EOFException("fixture exited: " + read(error));
    });
    try { return JsonParser.parseString(pending.get(90, TimeUnit.SECONDS)).getAsJsonObject(); }
    catch (TimeoutException timeout) { throw new AssertionError("fixture timeout: " + read(error), timeout); }
  }
  private static String required(String name) {
    String value = System.getProperty(name);
    if (value == null || value.isEmpty()) throw new IllegalStateException("missing " + name);
    return value;
  }
  private static String read(Path path) throws IOException { return new String(Files.readAllBytes(path), StandardCharsets.UTF_8); }
}
