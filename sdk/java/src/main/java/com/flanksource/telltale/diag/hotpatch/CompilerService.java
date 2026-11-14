package com.flanksource.telltale.diag.hotpatch;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.IOException;
import java.io.OutputStream;
import java.io.UncheckedIOException;
import java.net.URI;
import java.net.URL;
import java.net.URLClassLoader;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.security.CodeSource;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import javax.tools.Diagnostic;
import javax.tools.DiagnosticCollector;
import javax.tools.FileObject;
import javax.tools.ForwardingJavaFileManager;
import javax.tools.JavaCompiler;
import javax.tools.JavaFileManager;
import javax.tools.JavaFileObject;
import javax.tools.SimpleJavaFileObject;
import javax.tools.StandardJavaFileManager;
import javax.tools.ToolProvider;

/**
 * Compiles edited Java source against the classpath of the JVM this WAR runs in, producing the
 * class bytes {@link PatchService} redefines.
 *
 * <p>javac is loaded the same way the Attach API is: on Java 8 it lives in {@code tools.jar},
 * absent from a JRE image, so it comes from the JDK {@link InstrumentationService#findJdk()} finds
 * — including the one application CLI side-loads to {@code /tmp/jdk}. Unlike the Attach API, javac
 * is pure Java, so loading it into a child classloader is enough; no native library is involved.
 *
 * <p>The classpath is derived from the target class's own {@link CodeSource} rather than from
 * {@code URLClassLoader.getURLs()}, which TomEE's webapp loader does not usefully implement. It is
 * reported back on every result so a compile failure caused by a missing entry is diagnosable
 * instead of mysterious.
 */
public final class CompilerService {

  /** javac's entry point inside tools.jar, used when the JVM has no built-in compiler. */
  private static final String JAVAC_TOOL = "com.sun.tools.javac.api.JavacTool";

  /** Kept so repeated compiles do not re-open tools.jar. */
  private static volatile JavaCompiler compiler;

  /** What to compile: one top-level class's full source, resolved against one loader. */
  public static final class Request {
    private final String className;
    private final String source;
    private final ClassLoader loader;
    private final Class<?> target;
    private String classpath;

    public Request(String className, String source, Class<?> target) {
      if (className == null || className.trim().isEmpty()) {
        throw new IllegalArgumentException("class is required");
      }
      if (source == null || source.trim().isEmpty()) {
        throw new IllegalArgumentException("source is required for " + className);
      }
      this.className = className.trim();
      this.source = source;
      this.target = target;
      this.loader = target == null ? null : target.getClassLoader();
    }

    /** Overrides the derived classpath entirely; for when derivation gets it wrong. */
    public Request withClasspath(String value) {
      this.classpath = value == null || value.trim().isEmpty() ? null : value.trim();
      return this;
    }

    public String getClassName() {
      return className;
    }
  }

  /** One javac message, positioned in the submitted source. */
  public static final class Message {
    private final String kind;
    private final long line;
    private final long column;
    private final String code;
    private final String text;

    Message(String kind, long line, long column, String code, String text) {
      this.kind = kind;
      this.line = line;
      this.column = column;
      this.code = code;
      this.text = text;
    }

    public Map<String, Object> toMap() {
      Map<String, Object> out = new LinkedHashMap<>();
      out.put("kind", kind);
      out.put("line", line);
      out.put("column", column);
      out.put("code", code == null ? "" : code);
      out.put("message", text == null ? "" : text);
      return out;
    }

    public boolean isError() {
      return "ERROR".equals(kind);
    }

    @Override
    public String toString() {
      return kind + " " + line + ":" + column + " " + text;
    }
  }

  /**
   * The outcome of one compile. {@code classes} carries every class file javac produced — the
   * top-level class plus its nested and anonymous classes, all of which have to be redefined
   * together.
   */
  public static final class Result {
    private final boolean success;
    private final Map<String, byte[]> classes;
    private final List<Message> messages;
    private final String classpath;

    Result(boolean success, Map<String, byte[]> classes, List<Message> messages, String classpath) {
      this.success = success;
      this.classes = classes;
      this.messages = messages;
      this.classpath = classpath;
    }

    public boolean isSuccess() {
      return success;
    }

    public Map<String, byte[]> getClasses() {
      return Collections.unmodifiableMap(classes);
    }

    public List<Message> getMessages() {
      return Collections.unmodifiableList(messages);
    }

    public String getClasspath() {
      return classpath;
    }

    /** Contract shape for the compile/patch responses. */
    public Map<String, Object> toMap() {
      List<Map<String, Object>> diagnostics = new ArrayList<>();
      for (Message message : messages) {
        diagnostics.add(message.toMap());
      }
      Map<String, Object> out = new LinkedHashMap<>();
      out.put("success", success);
      out.put("classes", new ArrayList<>(classes.keySet()));
      out.put("diagnostics", diagnostics);
      out.put(
          "classpathEntries",
          classpath.isEmpty() ? 0 : classpath.split(File.pathSeparator, -1).length);
      return out;
    }
  }

  public Result compile(Request request) {
    String classpath = request.classpath != null ? request.classpath : classpathFor(request.target);
    JavaCompiler javac = compiler();
    DiagnosticCollector<JavaFileObject> collected = new DiagnosticCollector<>();
    Map<String, byte[]> output = new LinkedHashMap<>();

    StandardJavaFileManager standard =
        javac.getStandardFileManager(collected, Locale.ROOT, StandardCharsets.UTF_8);
    JavaFileManager capturing = new CapturingFileManager(standard, output);
    List<String> options =
        new ArrayList<>(
            Arrays.asList("-classpath", classpath, "-g", "-proc:none", "-Xlint:-options"));
    // This WAR patches Java 8 application classes. A JDK 21 test/diagnostic host
    // must not emit JDK 21 bytecode (or call newer JDK APIs) into a class
    // that the Java 8 target JVM has to redefine and run.
    if (javax.lang.model.SourceVersion.latestSupported()
            .compareTo(javax.lang.model.SourceVersion.RELEASE_8)
        > 0) {
      options.addAll(Arrays.asList("--release", "8"));
    } else {
      // javac 8 predates --release; the host API is already Java 8.
      options.addAll(Arrays.asList("-source", "8", "-target", "8"));
    }

    boolean ok;
    try {
      ok =
          Boolean.TRUE.equals(
              javac
                  .getTask(
                      null,
                      capturing,
                      collected,
                      options,
                      null,
                      Collections.singletonList(new SourceFile(request.className, request.source)))
                  .call());
    } catch (RuntimeException e) {
      throw new IllegalStateException(
          "compile of "
              + request.className
              + " failed inside javac: "
              + e
              + " (classpath had "
              + entryCount(classpath)
              + " entries)",
          e);
    } finally {
      closeQuietly(capturing);
    }

    List<Message> messages = new ArrayList<>();
    for (Diagnostic<? extends JavaFileObject> diagnostic : collected.getDiagnostics()) {
      messages.add(
          new Message(
              diagnostic.getKind().name(),
              diagnostic.getLineNumber(),
              diagnostic.getColumnNumber(),
              diagnostic.getCode(),
              diagnostic.getMessage(Locale.ROOT)));
    }
    // javac can report success with no output when the source declares a
    // different type than the caller named; that is a failure to us.
    if (ok && !output.containsKey(request.className)) {
      messages.add(
          new Message(
              "ERROR",
              -1,
              -1,
              "telltale.diag.wrong-class",
              "compiled successfully but produced no class named "
                  + request.className
                  + " (produced "
                  + output.keySet()
                  + ") — the source must declare that exact type"));
      ok = false;
    }
    return new Result(ok, output, messages, classpath);
  }

  /**
   * Builds the compile classpath from the target class's code source: for an exploded webapp that
   * is {@code WEB-INF/classes}, whose sibling {@code WEB-INF/lib} holds the jars the class was
   * compiled against. Any {@link URLClassLoader} ancestors that do answer {@code getURLs()} are
   * added too, and {@code java.class.path} last.
   */
  static String classpathFor(Class<?> target) {
    Set<String> entries = new LinkedHashSet<>();
    Path codeSource = codeSourceOf(target);
    if (codeSource != null) {
      entries.add(codeSource.toString());
      // <webapp>/WEB-INF/classes -> <webapp>/WEB-INF/lib/*.jar
      addJars(
          entries, codeSource.getParent() == null ? null : codeSource.getParent().resolve("lib"));
      // A class loaded from a jar sits beside the rest of them.
      if (codeSource.toString().endsWith(".jar")) {
        addJars(entries, codeSource.getParent());
      }
    }
    for (ClassLoader loader = target == null ? null : target.getClassLoader();
        loader != null;
        loader = parentOf(loader)) {
      addLoaderURLs(entries, loader);
    }
    for (String entry : System.getProperty("java.class.path", "").split(File.pathSeparator)) {
      if (!entry.trim().isEmpty()) {
        entries.add(entry.trim());
      }
    }
    return String.join(File.pathSeparator, entries);
  }

  private static Path codeSourceOf(Class<?> target) {
    if (target == null) {
      return null;
    }
    try {
      CodeSource source = target.getProtectionDomain().getCodeSource();
      if (source == null || source.getLocation() == null) {
        return null;
      }
      return Paths.get(source.getLocation().toURI());
    } catch (RuntimeException | java.net.URISyntaxException e) {
      // A non-file: code source (or a SecurityManager) simply contributes nothing.
      return null;
    }
  }

  private static void addJars(Set<String> entries, Path directory) {
    if (directory == null || !Files.isDirectory(directory)) {
      return;
    }
    try (java.util.stream.Stream<Path> children = Files.list(directory)) {
      children
          .filter(path -> path.toString().endsWith(".jar"))
          .sorted()
          .forEach(path -> entries.add(path.toString()));
    } catch (IOException e) {
      throw new UncheckedIOException("cannot list " + directory + " for the compile classpath", e);
    }
  }

  /**
   * TomEE's webapp loader throws from {@code getURLs()}, so this is best-effort by design: a loader
   * that cannot describe itself contributes nothing rather than failing the compile.
   */
  private static void addLoaderURLs(Set<String> entries, ClassLoader loader) {
    if (!(loader instanceof URLClassLoader)) {
      return;
    }
    URL[] urls;
    try {
      urls = ((URLClassLoader) loader).getURLs();
    } catch (RuntimeException | LinkageError e) {
      return;
    }
    if (urls == null) {
      return;
    }
    for (URL url : urls) {
      try {
        entries.add(Paths.get(url.toURI()).toString());
      } catch (RuntimeException | java.net.URISyntaxException ignored) {
        // Not a file: URL — nothing javac can read.
      }
    }
  }

  private static ClassLoader parentOf(ClassLoader loader) {
    try {
      return loader.getParent();
    } catch (RuntimeException e) {
      return null;
    }
  }

  static int entryCount(String classpath) {
    return classpath == null || classpath.isEmpty()
        ? 0
        : classpath.split(File.pathSeparator, -1).length;
  }

  /**
   * The JVM's own compiler when it has one (a JDK, or Java 9+), else javac out of the discovered
   * JDK's tools.jar.
   */
  private static JavaCompiler compiler() {
    JavaCompiler existing = compiler;
    if (existing != null) {
      return existing;
    }
    synchronized (CompilerService.class) {
      if (compiler == null) {
        compiler = loadCompiler();
      }
      return compiler;
    }
  }

  private static JavaCompiler loadCompiler() {
    JavaCompiler builtin = ToolProvider.getSystemJavaCompiler();
    if (builtin != null) {
      return builtin;
    }
    InstrumentationService.Jdk jdk = InstrumentationService.findJdk();
    if (jdk.toolsJar == null) {
      throw new IllegalStateException(
          "no java compiler: this JVM has none built in and "
              + jdk.home
              + " has no lib/tools.jar to load javac from");
    }
    try {
      URLClassLoader tools =
          new URLClassLoader(
              new URL[] {jdk.toolsJar.toUri().toURL()}, CompilerService.class.getClassLoader());
      Object tool = Class.forName(JAVAC_TOOL, true, tools).getMethod("create").invoke(null);
      return (JavaCompiler) tool;
    } catch (ReflectiveOperationException | IOException | RuntimeException e) {
      throw new IllegalStateException(
          "no java compiler: "
              + JAVAC_TOOL
              + " could not be loaded from "
              + jdk.toolsJar
              + ": "
              + e,
          e);
    }
  }

  private static void closeQuietly(JavaFileManager manager) {
    try {
      manager.close();
    } catch (IOException | RuntimeException ignored) {
      // The compile result is already in hand; a failed close must not mask it.
    }
  }

  /** The submitted source, handed to javac without ever touching the filesystem. */
  private static final class SourceFile extends SimpleJavaFileObject {
    private final String source;

    SourceFile(String className, String source) {
      super(
          URI.create("string:///" + className.replace('.', '/') + Kind.SOURCE.extension),
          Kind.SOURCE);
      this.source = source;
    }

    @Override
    public CharSequence getCharContent(boolean ignoreEncodingErrors) {
      return source;
    }
  }

  /** Collects javac's class output in memory, keyed by binary name. */
  private static final class CapturingFileManager
      extends ForwardingJavaFileManager<StandardJavaFileManager> {
    private final Map<String, byte[]> output;

    CapturingFileManager(StandardJavaFileManager delegate, Map<String, byte[]> output) {
      super(delegate);
      this.output = output;
    }

    @Override
    public JavaFileObject getJavaFileForOutput(
        Location location, String className, JavaFileObject.Kind kind, FileObject sibling) {
      return new ClassOutput(className, output);
    }
  }

  private static final class ClassOutput extends SimpleJavaFileObject {
    private final String className;
    private final Map<String, byte[]> output;

    ClassOutput(String className, Map<String, byte[]> output) {
      super(
          URI.create("bytes:///" + className.replace('.', '/') + Kind.CLASS.extension), Kind.CLASS);
      this.className = className;
      this.output = output;
    }

    @Override
    public OutputStream openOutputStream() {
      return new ByteArrayOutputStream() {
        @Override
        public void close() {
          output.put(className, toByteArray());
        }
      };
    }
  }
}
