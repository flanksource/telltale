package com.flanksource.telltale;

import com.google.gson.JsonObject;
import java.net.URI;
import java.nio.ByteBuffer;
import java.util.Collections;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;
import org.java_websocket.client.WebSocketClient;
import org.java_websocket.handshake.ServerHandshake;

final class Hub extends WebSocketClient {
  private final LinkedBlockingQueue<String> messages = new LinkedBlockingQueue<>(128);
  private volatile Exception failure;

  private Hub(URI uri, String token) {
    super(uri, Collections.singletonMap("Authorization", "Bearer " + token));
    setConnectionLostTimeout(30);
  }

  public void onOpen(ServerHandshake handshake) {}

  public void onMessage(String value) {
    if (!messages.offer(value)) {
      failure = new IllegalStateException("Hub input queue overflow");
      close();
    }
  }

  public void onMessage(ByteBuffer value) {
    failure = new IllegalArgumentException("Unexpected inbound binary frame");
    close();
  }

  public void onClose(int code, String reason, boolean remote) {
    if (failure == null) failure = new IllegalStateException("Hub connection closed");
  }

  public void onError(Exception error) {
    failure = error;
  }

  static Thread start(Agent agent, String address) {
    Thread thread =
        new Thread(
            () -> {
              int delay = 1;
              while (!agent.closed) {
                Session session = null;
                try {
                  URI base = URI.create(address);
                  String scheme =
                      base.getScheme().equals("https")
                          ? "wss"
                          : base.getScheme().equals("http") ? "ws" : base.getScheme();
                  URI uri =
                      new URI(
                          scheme,
                          null,
                          base.getHost(),
                          base.getPort(),
                          base.getPath().replaceAll("/$", "") + "/api/agents/connect",
                          null,
                          null);
                  Hub hub = new Hub(uri, agent.config.get("token").getAsString());
                  agent.hub = hub;
                  if (!hub.connectBlocking(10, TimeUnit.SECONDS))
                    throw new IllegalStateException("Hub connection timed out");
                  try (Session active = new Session(agent, hub.wire(), true)) {
                    session = active;
                    active.run();
                  } finally {
                    hub.close();
                  }
                } catch (Exception error) {
                  if (!agent.closed) System.err.println("telltale hub: " + error.getClass().getSimpleName());
                  else if (session != null && !session.stopped()) agent.stopFailure.compareAndSet(null, error);
                }
                if (agent.closed) return;
                try {
                  Thread.sleep(delay * 1000L);
                } catch (InterruptedException stop) {
                  Thread.currentThread().interrupt();
                  return;
                }
                delay = Math.min(30, delay * 2);
              }
            },
            "telltale-hub");
    thread.setDaemon(true);
    thread.start();
    return thread;
  }

  private Wire wire() {
    return new Wire() {
      JsonObject read() throws Exception {
        while (failure == null) {
          String frame = messages.poll(1, TimeUnit.SECONDS);
          if (frame != null) return Agent.JSON.fromJson(frame, JsonObject.class);
        }
        throw failure;
      }

      synchronized void send(String type, JsonObject body, byte[] blob) {
        JsonObject frame = new JsonObject();
        frame.addProperty("t", type);
        frame.add("body", body);
        Hub.this.send(Agent.JSON.toJson(frame));
        if (blob != null) Hub.this.send(blob);
      }
    };
  }
}
