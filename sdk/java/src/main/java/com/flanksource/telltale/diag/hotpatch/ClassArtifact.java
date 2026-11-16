package com.flanksource.telltale.diag.hotpatch;

import java.io.IOException;
import java.io.InputStream;
import java.net.URL;
import java.net.JarURLConnection;
import java.net.URISyntaxException;
import java.nio.file.Files;
import java.nio.file.NoSuchFileException;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.security.CodeSource;
import java.util.jar.JarEntry;
import java.util.jar.JarFile;

/** Reads defining artifacts without application loader resource calls; generated classes may have no entry. */
public final class ClassArtifact {
  private ClassArtifact() {}

  public static InputStream open(Class<?> type) throws IOException {
    String path = type.getName().replace('.', '/') + ".class";
    if (type.getClassLoader() == null) return type.getResourceAsStream("/" + path);
    CodeSource source = type.getProtectionDomain().getCodeSource();
    URL location = source == null ? null : source.getLocation();
    if (location == null) return null;
    String origin = location.toExternalForm();
    if (origin.startsWith("jrt:/")) return new URL(origin + "/" + path).openStream();
    if (!origin.startsWith("file:") && !origin.startsWith("jar:file:")) {
      throw new IOException("code-source location is not a local class artifact: " + location);
    }
    if (location.getProtocol().equals("file")) {
      Path root;
      try { root = Paths.get(location.toURI()); }
      catch (URISyntaxException error) { throw new IOException("invalid code-source location " + location, error); }
      if (Files.isDirectory(root)) {
        try { return Files.newInputStream(root.resolve(path)); }
        catch (NoSuchFileException generated) { return null; }
      }
    }
    URL jar = location.getProtocol().equals("jar") ? location : new URL("jar:" + origin + "!/");
    JarURLConnection connection = (JarURLConnection) jar.openConnection();
    connection.setUseCaches(true);
    JarFile artifact = connection.getJarFile();
    String prefix = connection.getEntryName();
    JarEntry entry = artifact.getJarEntry((prefix == null ? "" : prefix) + path);
    return entry == null ? null : artifact.getInputStream(entry);
  }
}
