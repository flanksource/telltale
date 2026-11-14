package com.flanksource.telltale;

import static org.junit.Assert.*;

import java.io.*;
import java.nio.file.*;
import java.util.*;
import java.util.jar.*;
import org.junit.Test;

public class ExtensionsTest {
  @Test
  public void doesNotPublishAnExtensionWhenItsManifestCannotBeSaved() throws Exception {
    Path directory =
        Files.createTempDirectory(
            Files.createDirectories(Paths.get(".tmp").toAbsolutePath()), "extension-persistence-");
    try (Extensions extensions = new Extensions(directory.toString())) {
      Files.createDirectory(directory.resolve("active.properties"));
      try {
        extensions.install(
            Collections.singletonMap("jar", Base64.getEncoder().encodeToString(exampleJar())),
            false);
        fail("accepted failed persistence");
      } catch (IOException expected) {
        assertTrue(extensions.list().isEmpty());
      }
    }
  }

  @Test
  public void installsPersistsReplacesAndUnloadsARealExtensionJar() throws Exception {
    Path directory =
        Files.createTempDirectory(
            Files.createDirectories(Paths.get(".tmp").toAbsolutePath()),
            "telltale-extension-test-");
    Map<String, Object> request =
        Collections.singletonMap("jar", Base64.getEncoder().encodeToString(exampleJar()));
    try (Extensions extensions = new Extensions(directory.toString())) {
      extensions.install(request, false);
      assertEquals(
          Collections.singletonMap("message", "Hello operator"),
          extensions.call(
              "extension.example.greet", Collections.singletonMap("name", "operator"), false));
      assertEquals("extension.example.greet", extensions.catalog().get(0).get("name"));
      try (Extensions.BindingsLease bindings = extensions.bindings(null)) {
        assertEquals(
            Collections.singletonMap("loaderType", "bootstrap"), bindings.values.get("example"));
      }
      try (Extensions.BindingsLease bindings = extensions.bindings(getClass().getClassLoader())) {
        assertTrue(bindings.values.containsKey("example"));
        try {
          extensions.unload("example");
          fail("busy extension unloaded");
        } catch (IllegalStateException expected) {
          assertTrue(expected.getMessage().contains("active"));
        }
        try {
          extensions.install(request, true);
          fail("busy extension replaced");
        } catch (IllegalStateException expected) {
          assertTrue(expected.getMessage().contains("active"));
        }
      }
      extensions.install(request, true);
    }
    try (Extensions restored = new Extensions(directory.toString())) {
      assertEquals(1, restored.list().size());
      restored.unload("example");
    }
    try (Extensions empty = new Extensions(directory.toString())) {
      assertTrue(empty.list().isEmpty());
    }
  }

  private byte[] exampleJar() throws Exception {
    ByteArrayOutputStream bytes = new ByteArrayOutputStream();
    try (JarOutputStream jar = new JarOutputStream(bytes)) {
      jar.putNextEntry(
          new JarEntry("META-INF/services/com.flanksource.telltale.DiagnosticExtension"));
      jar.write("example.ExampleExtension\n".getBytes(java.nio.charset.StandardCharsets.UTF_8));
      jar.closeEntry();
      jar.putNextEntry(new JarEntry("example/ExampleExtension.class"));
      try (InputStream input = getClass().getResourceAsStream("/example/ExampleExtension.class")) {
        byte[] buffer = new byte[4096];
        int count;
        while ((count = input.read(buffer)) != -1) jar.write(buffer, 0, count);
      }
      jar.closeEntry();
    }
    return bytes.toByteArray();
  }

  @Test
  public void installationRequiresConfiguredStorageAndPermission() throws Exception {
    Diagnostics runtime = new Diagnostics(Collections.singletonMap("allowMutation", true));
    try {
      runtime.call("jvm.extensions.install", Collections.emptyMap(), true);
      fail("installation without storage accepted");
    } catch (IllegalStateException expected) {
      assertTrue(expected.getMessage().contains("extensionsDir"));
    }
  }

  @Test
  public void ognlUsesTheSelectedLoaderAndMutationGate() throws Exception {
    Diagnostics runtime = new Diagnostics(Collections.singletonMap("allowMutation", true));
    assertEquals(
        Collections.singletonMap("value", 7),
        runtime.call(
            "jvm.ognl.eval",
            Collections.singletonMap("expression", "@java.lang.Math@addExact(3,4)"),
            true));
  }
}
