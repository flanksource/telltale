package com.flanksource.telltale;

import static org.junit.Assert.*;

import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import org.junit.Test;

public class CatalogSnapshotTest {
  @Test
  public void committedGoCatalogMatchesTheOperationRegistry() throws Exception {
    String path = System.getProperty("telltale.jvm.catalog");
    assertNotNull("telltale.jvm.catalog system property is required", path);
    String committed = new String(Files.readAllBytes(Paths.get(path)), StandardCharsets.UTF_8);
    assertEquals(
        path + " is stale: run ./sdk/java/gradlew -p sdk/java jvmCatalog",
        CatalogExport.render(),
        committed);
  }

  @Test
  public void everyBuiltInOperationIsDescribedAndServedAsItsOwnCommand() throws Exception {
    Set<String> commands = new HashSet<>();
    for (Map<String, Object> operation : new Diagnostics(Collections.emptyMap()).catalog()) {
      Object name = operation.get("name");
      assertFalse(
          name + " has no description",
          String.valueOf(operation.get("description")).trim().isEmpty());
      String command = String.valueOf(operation.get("command"));
      assertTrue(name + " command " + command, command.matches("jvm( [a-z]+)+"));
      assertTrue(name + " shares its command " + command, commands.add(command));
    }
  }
}
