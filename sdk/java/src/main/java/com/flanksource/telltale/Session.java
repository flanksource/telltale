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
  private final Map<String, Recording> recordings = new HashMap<>();
  private final Map<String, Long> sequences = new HashMap<>();
  private final Map<String, Long> transferred = new HashMap<>();
  private final Map<String, Long> byteLimits = new HashMap<>();
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
        if (!body.get("probe").getAsString().equals("jvm_jfr"))
          throw new IllegalArgumentException("Unsupported probe");
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
        recordings.put(id, new Recording(body.getAsJsonObject("params")));
        if (body.has("limits") && body.getAsJsonObject("limits").has("maxBytes"))
          byteLimits.put(id, body.getAsJsonObject("limits").get("maxBytes").getAsLong());
        if (delay > 0) {
          deadlines.schedule(
              () -> {
                synchronized (Session.this) {
                  if (!recordings.containsKey(id)) return;
                  JsonObject end = new JsonObject();
                  end.addProperty("session", id);
                  try (Recording r = recordings.remove(id)) {
                    sendBlob(id, "", r.dump());
                  } catch (Exception error) {
                    end.addProperty("error", error.toString());
                  }
                  try {
                    wire.send("ended", end, null);
                    sequences.remove(id);
                    transferred.remove(id);
                    byteLimits.remove(id);
                  } catch (Exception error) {
                    System.err.println(
                        "telltale deadline response failed: " + error.getClass().getSimpleName());
                  }
                }
              },
              delay,
              java.util.concurrent.TimeUnit.MILLISECONDS);
        }
      } else {
        Recording recording = recordings.get(id);
        if (recording == null) throw new IllegalArgumentException("Unknown session");
        if (kind.equals("checkpoint")) {
          String checkpoint = body.get("id").getAsString();
          response.addProperty("id", checkpoint);
          sendBlob(id, checkpoint, recording.dump());
        } else {
          try (Recording ending = recordings.remove(id)) {
            sendBlob(id, "", ending.dump());
          }
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
    }
  }

  private void sendBlob(String id, String checkpoint, byte[] data) throws Exception {
    long bytes = transferred.containsKey(id) ? transferred.get(id) : 0;
    long limit = byteLimits.containsKey(id) ? byteLimits.get(id) : 0;
    if (limit > 0 && data.length > limit - bytes)
      throw new IllegalStateException("JFR capture byte limit exceeded");
    transferred.put(id, bytes + data.length);
    long sequence = sequences.containsKey(id) ? sequences.get(id) + 1 : 1;
    sequences.put(id, sequence);
    JsonObject blob = new JsonObject();
    blob.addProperty("session", id);
    blob.addProperty("checkpoint", checkpoint);
    blob.addProperty("seq", sequence);
    blob.addProperty("name", "recording.jfr");
    blob.addProperty("contentType", "application/x-jfr");
    blob.addProperty("len", data.length);
    wire.send("blob", blob, data);
  }

  public synchronized void close() throws Exception {
    deadlines.shutdownNow();
    Exception failure = null;
    for (Recording r : recordings.values()) {
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
}
