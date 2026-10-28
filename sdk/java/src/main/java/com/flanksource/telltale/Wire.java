package com.flanksource.telltale;

import com.google.gson.JsonObject;
import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.net.Socket;
import java.nio.charset.StandardCharsets;

class Wire implements AutoCloseable {
  private final DataInputStream input;
  private final DataOutputStream output;

  Wire(Socket socket) throws Exception {
    input = new DataInputStream(socket.getInputStream());
    output = new DataOutputStream(socket.getOutputStream());
  }

  Wire() {
    input = null;
    output = null;
  }

  JsonObject read() throws Exception {
    int size = input.readInt();
    if (size < 0 || size > 64 * 1024 * 1024)
      throw new IllegalArgumentException("Invalid frame size");
    byte[] data = new byte[size];
    input.readFully(data);
    return Agent.JSON.fromJson(new String(data, StandardCharsets.UTF_8), JsonObject.class);
  }

  synchronized void send(String type, JsonObject body, byte[] blob) throws Exception {
    JsonObject frame = new JsonObject();
    frame.addProperty("t", type);
    frame.add("body", body);
    byte[] data = Agent.JSON.toJson(frame).getBytes(StandardCharsets.UTF_8);
    output.writeInt(data.length);
    output.write(data);
    if (blob != null) {
      output.writeInt(blob.length);
      output.write(blob);
    }
    output.flush();
  }

  public void close() throws Exception {
    if (input != null) input.close();
    if (output != null) output.close();
  }
}
