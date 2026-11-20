package com.flanksource.telltale.auth;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.security.PublicKey;
import java.util.Map;

/** Fetches a JWKS from an http(s) URL with bounded time and size. */
final class JwksSource {
  private static final int TIMEOUT_MS = 5000;
  private static final int MAX_BYTES = 1024 * 1024;
  private final URL url;

  JwksSource(String location) {
    try {
      url = new URL(location);
    } catch (IOException error) {
      throw new IllegalArgumentException("jwksUrl is not a valid URL: " + location, error);
    }
    if (!url.getProtocol().equals("http") && !url.getProtocol().equals("https"))
      throw new IllegalArgumentException("jwksUrl must use http or https: " + location);
  }

  Map<String, PublicKey> fetch() throws IOException {
    HttpURLConnection connection = (HttpURLConnection) url.openConnection();
    connection.setConnectTimeout(TIMEOUT_MS);
    connection.setReadTimeout(TIMEOUT_MS);
    connection.setInstanceFollowRedirects(false);
    connection.setRequestProperty("Accept", "application/json");
    try {
      int status = connection.getResponseCode();
      if (status != 200) throw new IOException(url + " answered HTTP " + status);
      try (InputStream input = connection.getInputStream()) {
        return Jwks.parse(new String(read(input), StandardCharsets.UTF_8), url.toString());
      } catch (IllegalArgumentException invalid) {
        throw new IOException(invalid.getMessage(), invalid);
      }
    } finally {
      connection.disconnect();
    }
  }

  private static byte[] read(InputStream input) throws IOException {
    ByteArrayOutputStream result = new ByteArrayOutputStream();
    byte[] buffer = new byte[8192];
    int count;
    while ((count = input.read(buffer)) != -1) {
      if (count > MAX_BYTES - result.size()) throw new IOException("JWKS exceeds 1 MiB");
      result.write(buffer, 0, count);
    }
    return result.toByteArray();
  }
}
