package com.flanksource.telltale.diag.jmx;

import static org.junit.jupiter.api.Assertions.*;

import java.util.logging.Level;
import java.util.logging.Logger;
import org.junit.jupiter.api.Test;

public class LoggingServiceTest {
  @Test
  void readsAndChangesLevelsIncludingInheritanceWithoutCreatingUnknownLoggers() {
    Logger logger = Logger.getLogger("example.diagnostics.test");
    Level previous = logger.getLevel();
    LoggingService service = new LoggingService();
    try {
      service.setLevel(logger.getName(), "FINE");
      assertEquals(
          "FINE",
          service.loggers().stream()
              .filter(row -> logger.getName().equals(row.get("name")))
              .findFirst()
              .get()
              .get("level"));
      service.setLevel(logger.getName(), null);
      assertNull(logger.getLevel());
      assertThrows(
          IllegalArgumentException.class, () -> service.setLevel(logger.getName(), "invalid"));
      assertThrows(
          IllegalArgumentException.class,
          () -> service.setLevel("example.diagnostics.missing", "INFO"));
    } finally {
      logger.setLevel(previous);
    }
  }
}
