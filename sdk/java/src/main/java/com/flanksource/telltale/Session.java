package com.flanksource.telltale;

import com.google.gson.JsonObject;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.HashMap;
import java.util.Map;

final class Session implements AutoCloseable {
  private final Agent agent;
  private final Wire wire;
  private final boolean remote;
  private final java.util.concurrent.ThreadPoolExecutor calls =
      new java.util.concurrent.ThreadPoolExecutor(
          4,
          4,
          0,
          java.util.concurrent.TimeUnit.SECONDS,
          new java.util.concurrent.ArrayBlockingQueue<Runnable>(64),
          runnable -> {
            Thread thread = new Thread(runnable, "telltale-operation");
            thread.setDaemon(true);
            return thread;
          });
  private final Map<String, Capture> recordings = new HashMap<>();
  private final Map<String, Long> sequences = new HashMap<>();
  private final Map<String, Long> transferred = new HashMap<>();
  private final Map<String, Long> byteLimits = new HashMap<>();
  private final Map<String, Map<String, Object>> reported = new HashMap<>();
  private final Map<String, java.util.List<java.util.concurrent.ScheduledFuture<?>>> scheduled =
      new HashMap<>();
  private final java.util.concurrent.ScheduledExecutorService deadlines =
      java.util.concurrent.Executors.newSingleThreadScheduledExecutor(
          r -> {
            Thread thread = new Thread(r, "telltale-deadline");
            thread.setDaemon(true);
            return thread;
          });

  Session(Agent a, Wire w, boolean r) {
    agent = a;
    wire = w;
    remote = r;
  }

  void run() throws Exception {
    try {
      JsonObject welcome = wire.read();
      if (!welcome.get("t").getAsString().equals("welcome"))
        throw new IllegalArgumentException("Expected welcome");
      String token = welcome.getAsJsonObject("body").get("token").getAsString();
      boolean accepted =
          MessageDigest.isEqual(
              token.getBytes(StandardCharsets.UTF_8),
              agent.config.get("token").getAsString().getBytes(StandardCharsets.UTF_8));
      JsonObject ack = new JsonObject();
      ack.addProperty("accepted", accepted);
      wire.send("auth", ack, null);
      if (!accepted) throw new IllegalArgumentException("Authentication rejected");
      wire.send("hello", agent.hello(remote), null);
      while (true) dispatch(wire.read());
    } finally {
      close();
    }
  }

  private synchronized void dispatch(JsonObject frame) throws Exception {
    String kind = frame.get("t").getAsString();
    JsonObject body = frame.getAsJsonObject("body");
    if (kind.equals("call")) {
      try {
        calls.execute(() -> operation(body));
      } catch (java.util.concurrent.RejectedExecutionException error) {
        JsonObject result = new JsonObject();
        result.add("callId", body.get("callId"));
        result.addProperty("error", "too many active or queued operations");
        wire.send("result", result, null);
      }
      return;
    }
    if (kind.equals("ping")) {
      wire.send("pong", new JsonObject(), null);
      return;
    }
    String id = body.has("session") ? body.get("session").getAsString() : "";
    JsonObject response = new JsonObject();
    response.addProperty("session", id);
    String reply;
    if (kind.equals("arm")) reply = "armed";
    else if (kind.equals("checkpoint")) reply = "checkpointed";
    else if (kind.equals("disarm")) reply = "ended";
    else throw new IllegalArgumentException("Unsupported frame: " + kind);
    try {
      if (kind.equals("arm")) {
        String probe = body.get("probe").getAsString();
        if (!java.util.Arrays.asList(
                "jvm_jfr", "jvm_trace", "jvm_redefine", "jvm_notifications", "jvm_ognl")
            .contains(probe)) throw new IllegalArgumentException("Unsupported probe");
        if (recordings.containsKey(id)) throw new IllegalArgumentException("Duplicate session");
        long delay = 0;
        if (body.has("stopAt")) {
          delay =
              java.time.Instant.parse(body.get("stopAt").getAsString()).toEpochMilli()
                  - System.currentTimeMillis();
          if (delay <= 0) throw new IllegalArgumentException("Capture deadline already passed");
        }
        if (body.has("limits")
            && body.getAsJsonObject("limits").has("maxBytes")
            && body.getAsJsonObject("limits").get("maxBytes").getAsLong() < 0)
          throw new IllegalArgumentException("Capture byte limit must not be negative");
        Capture capture =
            agent.diagnostics.capture(
                probe,
                parameters(body),
                id,
                body.has("allowMutation") && body.get("allowMutation").getAsBoolean());
        recordings.put(id, capture);
        java.util.List<java.util.concurrent.ScheduledFuture<?>> jobs = new java.util.ArrayList<>();
        scheduled.put(id, jobs);
        jobs.add(
            deadlines.scheduleWithFixedDelay(
                () -> {
                  synchronized (Session.this) {
                    if (!recordings.containsKey(id)) return;
                    try {
                      capture.renew();
                    } catch (Exception error) {
                      failCapture(id, error);
                    }
                  }
                },
                30,
                30,
                java.util.concurrent.TimeUnit.SECONDS));
        if (capture.streaming())
          jobs.add(
              deadlines.scheduleWithFixedDelay(
                  () -> {
                    synchronized (Session.this) {
                      if (!recordings.containsKey(id)) return;
                      try {
                        sendBlob(id, "", capture, capture.dump());
                        if (capture.complete()) {
                          finishCapture(id);
                          JsonObject end = new JsonObject();
                          end.addProperty("session", id);
                          end.addProperty("complete", true);
                          wire.send("ended", end, null);
                        }
                      } catch (Exception error) {
                        failCapture(id, error);
                      }
                    }
                  },
                  250,
                  250,
                  java.util.concurrent.TimeUnit.MILLISECONDS));
        if (body.has("limits") && body.getAsJsonObject("limits").has("maxBytes"))
          byteLimits.put(id, body.getAsJsonObject("limits").get("maxBytes").getAsLong());
        if (delay > 0) {
          jobs.add(
              deadlines.schedule(
                  () -> {
                    synchronized (Session.this) {
                      if (!recordings.containsKey(id)) return;
                      JsonObject end = new JsonObject();
                      end.addProperty("session", id);
                      try {
                        finishCapture(id);
                        end.addProperty("complete", true);
                      } catch (Exception error) {
                        end.addProperty("error", error.toString());
                      }
                      try {
                        wire.send("ended", end, null);
                        sequences.remove(id);
                        transferred.remove(id);
                        byteLimits.remove(id);
                        reported.remove(id);
                        cancelJobs(id);
                      } catch (Exception error) {
                        System.err.println(
                            "telltale deadline response failed: "
                                + error.getClass().getSimpleName());
                      }
                    }
                  },
                  delay,
                  java.util.concurrent.TimeUnit.MILLISECONDS));
        }
      } else {
        Capture recording = recordings.get(id);
        if (recording == null) throw new IllegalArgumentException("Unknown session");
        if (kind.equals("checkpoint")) {
          String checkpoint = body.get("id").getAsString();
          response.addProperty("id", checkpoint);
          sendBlob(id, checkpoint, recording, recording.dump());
        } else {
          finishCapture(id);
        }
      }
    } catch (Exception error) {
      while (error instanceof java.lang.reflect.InvocationTargetException
          && error.getCause() instanceof Exception) error = (Exception) error.getCause();
      response.addProperty("error", error.toString());
      if (kind.equals("checkpoint") && body.has("id")) response.add("id", body.get("id"));
    }
    wire.send(reply, response, null);
    if (kind.equals("disarm")) {
      sequences.remove(id);
      transferred.remove(id);
      byteLimits.remove(id);
      reported.remove(id);
      cancelJobs(id);
    }
  }

  private void sendBlob(String id, String checkpoint, Capture capture, byte[] data)
      throws Exception {
    long bytes = transferred.containsKey(id) ? transferred.get(id) : 0;
    long limit = byteLimits.containsKey(id) ? byteLimits.get(id) : 0;
    if (limit > 0 && data.length > limit - bytes)
      throw new IllegalStateException("Capture byte limit exceeded");
    transferred.put(id, bytes + data.length);
    long sequence = sequences.containsKey(id) ? sequences.get(id) + 1 : 1;
    sequences.put(id, sequence);
    JsonObject blob = new JsonObject();
    blob.addProperty("session", id);
    blob.addProperty("checkpoint", checkpoint);
    blob.addProperty("seq", sequence);
    blob.addProperty("name", capture.fileName());
    blob.addProperty("contentType", capture.contentType());
    blob.addProperty("len", data.length);
    wire.send("blob", blob, data);
    sendReport(id, capture);
  }

  /** Sends the capture's report after the blob it describes, when it changed since the last. */
  private void sendReport(String id, Capture capture) throws Exception {
    Map<String, Object> summary = capture.report();
    if (summary == null || summary.equals(reported.get(id))) return;
    reported.put(id, summary);
    JsonObject report = new JsonObject();
    report.addProperty("session", id);
    report.add("summary", Agent.JSON.toJsonTree(summary));
    wire.send("report", report, null);
  }

  private void finishCapture(String id) throws Exception {
    try (Capture capture = recordings.remove(id)) {
      if (capture == null) throw new IllegalArgumentException("Unknown session " + id);
      sendBlob(id, "", capture, capture.finish());
    } finally {
      cancelJobs(id);
      sequences.remove(id);
      transferred.remove(id);
      byteLimits.remove(id);
      reported.remove(id);
    }
  }

  public synchronized void close() throws Exception {
    calls.shutdownNow();
    deadlines.shutdownNow();
    Exception failure = null;
    for (Capture r : recordings.values()) {
      try {
        r.close();
      } catch (Exception e) {
        if (failure == null) failure = e;
        else failure.addSuppressed(e);
      }
    }
    recordings.clear();
    if (failure != null) throw failure;
  }

  private void failCapture(String id, Exception error) {
    cancelJobs(id);
    sequences.remove(id);
    transferred.remove(id);
    byteLimits.remove(id);
    reported.remove(id);
    Capture capture = recordings.remove(id);
    if (capture != null) {
      try {
        capture.close();
      } catch (Exception cleanup) {
        error.addSuppressed(cleanup);
      }
    }
    JsonObject result = new JsonObject();
    result.addProperty("session", id);
    result.addProperty("error", error.toString());
    try {
      wire.send("ended", result, null);
    } catch (Exception failure) {
      System.err.println("telltale capture failure: " + failure);
    }
  }

  private void cancelJobs(String id) {
    java.util.List<java.util.concurrent.ScheduledFuture<?>> jobs = scheduled.remove(id);
    if (jobs != null) for (java.util.concurrent.ScheduledFuture<?> job : jobs) job.cancel(false);
  }

  private void operation(JsonObject body) {
    JsonObject result = new JsonObject();
    result.add("callId", body.get("callId"));
    try {
      Object value =
          agent.diagnostics.call(
              body.get("op").getAsString(),
              parameters(body),
              body.has("allowMutation") && body.get("allowMutation").getAsBoolean());
      result.add("value", Agent.JSON.toJsonTree(value));
    } catch (Exception error) {
      result.addProperty("error", error.toString());
    }
    try {
      wire.send("result", result, null);
    } catch (Exception error) {
      System.err.println("telltale operation response failed: " + error);
    }
  }

  @SuppressWarnings("unchecked")
  private static Map<String, Object> parameters(JsonObject body) throws Exception {
    if (!body.has("params")) return new java.util.LinkedHashMap<>();
    Map<String, Object> params =
        com.flanksource.telltale.diag.Json.read(
            new java.io.ByteArrayInputStream(
                body.get("params").toString().getBytes(StandardCharsets.UTF_8)),
            Map.class);
    if (params == null) throw new IllegalArgumentException("params must be an object");
    return params;
  }
}
