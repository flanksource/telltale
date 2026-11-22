package com.flanksource.telltale;

import static org.junit.jupiter.api.Assertions.*;

import com.google.gson.Gson;
import com.google.gson.JsonArray;
import com.google.gson.JsonObject;
import java.nio.charset.StandardCharsets;
import java.util.LinkedHashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;

final class ExpressionCaptureTest {
  @Test
  void checkpointOnlyCapturesReadAtEachCheckpointAndNeverAtFinish() throws Exception {
    Map<String, Object> params = params("6 * 7");
    params.put("untilStopped", true);
    try (ExpressionCapture capture = new ExpressionCapture(params, getClass().getClassLoader())) {
      assertFalse(capture.streaming());
      for (int checkpoint = 0; checkpoint < 2; checkpoint++) {
        JsonArray rows = rows(capture.dump());
        assertEquals(1, rows.size());
        assertEquals("6 * 7", rows.get(0).getAsJsonObject().get("expression").getAsString());
      }
      assertEquals(0, rows(capture.finish()).size());
    }
  }

  @Test
  void checkpointOnlyCapturesRejectAnInterval() {
    Map<String, Object> params = params("6 * 7");
    params.put("untilStopped", true);
    params.put("intervalMs", 1000);
    assertThrows(IllegalArgumentException.class, () -> new ExpressionCapture(params, getClass().getClassLoader()));
  }

  @Test
  void evaluationErrorsAreRowsAndDoNotEndTheCapture() throws Exception {
    try (ExpressionCapture capture = new ExpressionCapture(params("6 +"), getClass().getClassLoader())) {
      JsonObject row = rows(capture.dump()).get(0).getAsJsonObject();
      assertEquals("6 +", row.get("expression").getAsString());
      assertFalse(row.get("error").getAsString().isEmpty());
      assertFalse(row.has("result"));
    }
  }

  private static Map<String, Object> params(String expression) {
    Map<String, Object> params = new LinkedHashMap<>();
    params.put("expression", expression);
    return params;
  }

  private static JsonArray rows(byte[] data) {
    return new Gson().fromJson(new String(data, StandardCharsets.UTF_8), JsonArray.class);
  }
}
