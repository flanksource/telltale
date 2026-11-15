package com.flanksource.telltale;

import com.google.gson.GsonBuilder;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.Collections;

/** Writes the built-in operation catalog that Telltale's typed JVM commands are generated from. */
public final class CatalogExport {
  static String render() throws Exception {
    return new GsonBuilder()
            .setPrettyPrinting()
            .disableHtmlEscaping()
            .create()
            .toJson(new Diagnostics(Collections.emptyMap()).catalog())
        + "\n";
  }

  public static void main(String[] args) throws Exception {
    if (args.length != 1) throw new IllegalArgumentException("usage: CatalogExport <catalog.json>");
    Files.write(Paths.get(args[0]), render().getBytes(StandardCharsets.UTF_8));
  }
}
