package observerfixture;

import com.google.gson.Gson;
import com.sun.net.httpserver.HttpServer;
import io.opentelemetry.api.common.AttributeKey;
import io.opentelemetry.api.GlobalOpenTelemetry;
import io.opentelemetry.api.OpenTelemetry;
import io.opentelemetry.api.common.Attributes;
import io.opentelemetry.api.common.AttributesBuilder;
import io.opentelemetry.api.trace.Span;
import io.opentelemetry.context.Context;
import io.opentelemetry.instrumentation.api.instrumenter.AttributesExtractor;
import io.opentelemetry.instrumentation.api.instrumenter.Instrumenter;
import io.opentelemetry.instrumentation.api.instrumenter.SpanKindExtractor;
import io.opentelemetry.sdk.OpenTelemetrySdk;
import io.opentelemetry.exporter.otlp.http.trace.OtlpHttpSpanExporter;
import io.opentelemetry.proto.collector.trace.v1.ExportTraceServiceRequest;
import io.opentelemetry.sdk.trace.SdkTracerProvider;
import io.opentelemetry.sdk.trace.export.BatchSpanProcessor;
import io.opentelemetry.sdk.trace.samplers.Sampler;
import java.io.*;
import java.lang.management.ManagementFactory;
import java.net.*;
import java.util.*;

/** Starts with only the official agent; Telltale contracts load after external attach. */
public final class NoRestartOtelFixture {
  private static final long PRECISE = 9007199254740993L;
  private static final class WireCollector {
    final List<ExportTraceServiceRequest> requests = Collections.synchronizedList(new ArrayList<>());
    void accept(com.sun.net.httpserver.HttpExchange exchange) throws IOException {
      try {
        requests.add(ExportTraceServiceRequest.parseFrom(exchange.getRequestBody()));
        exchange.getResponseHeaders().set("Content-Type", "application/x-protobuf");
        exchange.sendResponseHeaders(200, -1);
      } finally { exchange.close(); }
    }
    int httpCount() {
      int count = 0;
      synchronized (requests) {
        for (ExportTraceServiceRequest request : requests) {
          String scope = request.getResourceSpans(0).getScopeSpans(0).getScope().getName();
          if (scope.equals("gate-http") || scope.equals("io.opentelemetry.http-url-connection")) count++;
        }
      }
      return count;
    }
    List<ExportTraceServiceRequest> snapshot() {
      synchronized (requests) { return new ArrayList<>(requests); }
    }
  }

  public static void main(String[] args) throws Exception {
    boolean sampled = Boolean.parseBoolean(args[0]);
    HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", Integer.parseInt(args[1])), 0);
    WireCollector collector = new WireCollector();
    WireCollector official = new WireCollector();
    server.createContext("/v1/traces", collector::accept);
    server.createContext("/official/v1/traces", official::accept);
    OtlpHttpSpanExporter exporter = OtlpHttpSpanExporter.builder()
        .setEndpoint("http://127.0.0.1:" + server.getAddress().getPort() + "/v1/traces").build();
    SdkTracerProvider provider = SdkTracerProvider.builder()
        .setSampler(sampled ? Sampler.alwaysOn() : Sampler.alwaysOff())
        .addSpanProcessor(BatchSpanProcessor.builder(exporter)
            .setScheduleDelay(java.time.Duration.ofHours(1)).build()).build();
    OpenTelemetrySdk otel = OpenTelemetrySdk.builder().setTracerProvider(provider).build();
    Instrumenter<String, Void> instrumenter = instrumenter(GlobalOpenTelemetry.get());
    server.createContext("/gate", exchange -> {
      byte[] body = "gate-body".getBytes(java.nio.charset.StandardCharsets.UTF_8);
      exchange.sendResponseHeaders(200, body.length);
      exchange.getResponseBody().write(body);
      exchange.close();
    });
    server.start();
    URL url = new URL("http://127.0.0.1:" + server.getAddress().getPort() + "/gate");
    try {
      NoRestartSqlState.open();
      operation(instrumenter, url, "before", otel, null);
      flush(provider);
      awaitOfficial(official, sampled ? 2 : 0);
      boolean absent;
      try { Class.forName("com.flanksource.telltale.observer.ObserverRuntime"); absent = false; }
      catch (ClassNotFoundException expected) { absent = true; }
      Map<String, Object> result = new LinkedHashMap<>();
      result.put("pid", ManagementFactory.getRuntimeMXBean().getName().split("@")[0]);
      result.put("telltaleAbsent", absent);
      result.put("javaVersion", System.getProperty("java.version"));
      result.put("sampled", sampled);
      result.put("before", collector.requests.size());
      result.put("officialBefore", official.httpCount());
      System.out.println("READY " + new Gson().toJson(result));
      if (!"CAPTURE".equals(new BufferedReader(new InputStreamReader(System.in, "UTF-8")).readLine()))
        throw new IllegalStateException("expected CAPTURE after attach");
      Class<?> capture = Class.forName("observerfixture.NoRestartOtelCapture");
      Object armed = capture.getMethod("arm").invoke(null);
      operation(instrumenter, url, "during", otel, armed);
      flush(provider);
      awaitOfficial(official, sampled ? 4 : 0);
      result.put("during", collector.requests.size());
      result.put("officialDuring", official.httpCount());
      Class<?> sql = Class.forName("observerfixture.NoRestartSqlCapture");
      for (Map.Entry<?, ?> entry : ((Map<?, ?>) sql.getMethod("run").invoke(null)).entrySet())
        result.put(String.class.cast(entry.getKey()), entry.getValue());
      for (Map.Entry<?, ?> entry : ((Map<?, ?>) capture.getMethod("finish").invoke(armed)).entrySet())
        result.put(String.class.cast(entry.getKey()), entry.getValue());
      operation(instrumenter, url, "after", otel, null);
      flush(provider);
      awaitOfficial(official, sampled ? 6 : 0);
      result.put("after", collector.requests.size());
      result.put("officialAfter", official.httpCount());
      result.put("afterRows", capture.getMethod("afterRows").invoke(armed));
      List<String> names = new ArrayList<>();
      for (ExportTraceServiceRequest request : collector.requests)
        names.add(request.getResourceSpans(0).getScopeSpans(0).getSpans(0).getName());
      result.put("exportedNames", names);
      if (sampled) result.put("wireDuring", Base64.getEncoder().encodeToString(collector.requests.get(1).toByteArray()));
      if (sampled) {
        List<ExportTraceServiceRequest> http = new ArrayList<>();
        for (ExportTraceServiceRequest request : official.snapshot())
          if (request.getResourceSpans(0).getScopeSpans(0).getScope().getName().equals("io.opentelemetry.http-url-connection"))
            http.add(request);
        if (http.size() != 3) throw new IllegalStateException("original HTTP exporter delivery changed: " + http.size());
        result.put("officialWireDuring", Base64.getEncoder().encodeToString(http.get(1).toByteArray()));
      }
      result.put("pid", ManagementFactory.getRuntimeMXBean().getName().split("@")[0]);
      System.out.println("RESULT " + new Gson().toJson(result));
    } finally { try { NoRestartSqlState.close(); } finally { server.stop(0); provider.close(); } }
  }

  private static Instrumenter<String, Void> instrumenter(OpenTelemetry otel) {
    return Instrumenter.<String, Void>builder(otel, "gate-http", request -> "GET /instrumenter/" + request)
        .addAttributesExtractor(new AttributesExtractor<String, Void>() {
          public void onStart(AttributesBuilder attrs, Context parent, String request) {
            attrs.put("gate.method", "GET");
            attrs.put("url.path", "/gate/" + request);
            attrs.put("gate.precise", PRECISE);
          }
          public void onEnd(AttributesBuilder attrs, Context context, String request, Void response, Throwable error) {
            attrs.put("gate.status_code", 200L);
          }
        }).buildInstrumenter(SpanKindExtractor.alwaysClient());
  }

  private static void operation(Instrumenter<String, Void> instrumenter, URL url, String phase, OpenTelemetrySdk nonglobal, Object capture) throws Exception {
    Context parent = Context.root();
    if (!instrumenter.shouldStart(parent, phase)) throw new IllegalStateException("old instrumenter stopped");
    Context context = instrumenter.start(parent, phase);
    Span sentinel = nonglobal.getTracer("gate-sentinel").spanBuilder("GET /gate/" + phase)
        .setAttribute("gate.precise", PRECISE).startSpan();
    sentinel.addEvent("gate-event", Attributes.of(AttributeKey.longKey("gate.precise"), PRECISE));
    HttpURLConnection connection = (HttpURLConnection) url.openConnection();
    try {
      connection.setConnectTimeout(5000); connection.setReadTimeout(5000);
      if (connection.getResponseCode() != 200) throw new IllegalStateException("HTTP response changed");
      if (capture != null) capture.getClass().getMethod("sample").invoke(capture);
      try (InputStream body = connection.getInputStream(); ByteArrayOutputStream bytes = new ByteArrayOutputStream()) {
        byte[] buffer = new byte[64];
        for (int count; (count = body.read(buffer)) != -1;) bytes.write(buffer, 0, count);
        if (!"gate-body".equals(bytes.toString("UTF-8"))) throw new IllegalStateException("HTTP body changed");
      }
    } finally { connection.disconnect(); sentinel.end(); instrumenter.end(context, phase, null, null); }
  }

  private static void flush(SdkTracerProvider provider) {
    if (!provider.forceFlush().join(15, java.util.concurrent.TimeUnit.SECONDS).isSuccess())
      throw new IllegalStateException("existing OTLP exporter failed to flush");
  }

  private static void awaitOfficial(WireCollector collector, int expected) throws InterruptedException {
    long deadline = System.nanoTime() + java.util.concurrent.TimeUnit.SECONDS.toNanos(15);
    while (collector.httpCount() < expected && System.nanoTime() < deadline) Thread.sleep(20);
    if (collector.httpCount() != expected)
      throw new IllegalStateException("official HTTP exporter expected " + expected + " requests, got " + collector.httpCount());
  }
}
