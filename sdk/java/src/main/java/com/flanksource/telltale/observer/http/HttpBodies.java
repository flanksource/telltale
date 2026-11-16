package com.flanksource.telltale.observer.http;

import java.nio.ByteBuffer;
import java.nio.charset.*;
import java.util.*;

final class HttpBodies {
  private HttpBodies() {}
  static void content(Map<String, Object> body, byte[] bytes) {
    try {
      body.put("text", StandardCharsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT).onUnmappableCharacter(CodingErrorAction.REPORT).decode(ByteBuffer.wrap(bytes)).toString());
      body.remove("encoding");
    } catch (CharacterCodingException binary) {
      body.put("text", Base64.getEncoder().encodeToString(bytes)); body.put("encoding", "base64");
    }
  }
}
