package com.flanksource.telltale;

import com.google.gson.JsonObject;
import java.io.*;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;

/** A test HTTP exchange against an adapter (WAR or agent listener). */
final class HttpReply {
  final int status;
  final String body;
  final HttpURLConnection connection;

  private HttpReply(int status, String body, HttpURLConnection connection) {
    this.status = status;
    this.body = body;
    this.connection = connection;
  }

  String header(String name) {
    return connection.getHeaderField(name);
  }

  JsonObject json() {
    return Agent.JSON.fromJson(body, JsonObject.class);
  }

  static HttpReply send(String url, String method, String body, String token) throws Exception {
    return sendHeaders(
        url,
        method,
        body,
        token == null
            ? java.util.Collections.<String, String>emptyMap()
            : java.util.Collections.singletonMap("Authorization", "Bearer " + token));
  }

  static HttpReply sendHeaders(
          String url, String method, String body, java.util.Map<String, String> headers)
      throws Exception {
    HttpURLConnection connection = (HttpURLConnection) new URL(url).openConnection();
    connection.setConnectTimeout(5000);
    connection.setReadTimeout(30000);
    connection.setRequestMethod(method);
    for (java.util.Map.Entry<String, String> header : headers.entrySet())
      connection.setRequestProperty(header.getKey(), header.getValue());
    if (body != null) {
      connection.setDoOutput(true);
      connection.setRequestProperty("Content-Type", "application/json");
      try (OutputStream output = connection.getOutputStream()) {
        output.write(body.getBytes(StandardCharsets.UTF_8));
      }
    }
    int status = connection.getResponseCode();
    ByteArrayOutputStream bytes = new ByteArrayOutputStream();
    InputStream input = status >= 400 ? connection.getErrorStream() : connection.getInputStream();
    if (input != null)
      try (InputStream stream = input) {
        byte[] buffer = new byte[4096];
        int count;
        while ((count = stream.read(buffer)) != -1) bytes.write(buffer, 0, count);
      }
    return new HttpReply(status, new String(bytes.toByteArray(), StandardCharsets.UTF_8), connection);
  }
}
