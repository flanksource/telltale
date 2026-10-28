package com.flanksource.telltale;

import static org.junit.Assert.*;

import com.google.gson.JsonObject;
import org.junit.Test;

public class RecordingTest {
  @Test
  public void dumpsARealJfrRecording() throws Exception {
    JsonObject options = new JsonObject();
    options.addProperty("engine", "jfr");
    try (Recording recording = new Recording(options)) {
      Thread.sleep(100);
      byte[] data = recording.dump();
      assertTrue(data.length > 4);
      assertArrayEquals(new byte[] {'F', 'L', 'R', 0}, java.util.Arrays.copyOf(data, 4));
    }
  }

  @Test(expected = IllegalArgumentException.class)
  public void refusesAnUnknownEngine() throws Exception {
    JsonObject options = new JsonObject();
    options.addProperty("engine", "missing");
    new Recording(options);
  }

  @Test
  public void refusesDumpAfterClose() throws Exception {
    JsonObject options = new JsonObject();
    options.addProperty("engine", "jfr");
    Recording recording = new Recording(options);
    recording.close();
    try {
      recording.dump();
      fail("closed recording must refuse a dump");
    } catch (IllegalStateException expected) {
      assertEquals("Recording is closed", expected.getMessage());
    }
  }

  @Test
  public void profilesWallTimeWithTheBundledNativeLibrary() throws Exception {
    JsonObject options = new JsonObject();
    options.addProperty("engine", "async-profiler");
    options.addProperty("event", "wall");
    options.addProperty("interval", "1ms");
    try (Recording recording = new Recording(options)) {
      Thread.sleep(200);
      byte[] data = recording.dump();
      assertTrue(data.length > 4);
      assertArrayEquals(new byte[] {'F', 'L', 'R', 0}, java.util.Arrays.copyOf(data, 4));
      try {
        new Recording(options);
        fail("only one capture may own async-profiler");
      } catch (IllegalStateException expected) {
        assertTrue(expected.getMessage().contains("another Telltale capture"));
      }
      assertTrue(recording.dump().length > 4);
    }
    try (Recording recording = new Recording(options)) {
      assertTrue(recording.dump().length > 4);
    }
  }
}
