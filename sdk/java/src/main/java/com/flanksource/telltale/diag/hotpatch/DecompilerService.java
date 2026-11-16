package com.flanksource.telltale.diag.hotpatch;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collection;
import java.util.Collections;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.NavigableMap;
import java.util.TreeMap;
import org.benf.cfr.reader.api.CfrDriver;
import org.benf.cfr.reader.api.OutputSinkFactory;
import org.benf.cfr.reader.api.SinkReturns;

/**
 * Decompiles live class bytes with CFR, mirroring Arthas's {@code Decompiler} (CfrDriver + an
 * in-memory {@link OutputSinkFactory}).
 *
 * <p>CFR 0.152 is the newest release on Maven Central and the version Arthas pins; its own class
 * files are major 50, so it loads on the Temurin 8 JVM this WAR is hot-deployed into.
 *
 * <p>{@code CfrDriver.analyse} takes file paths, so the bytes are written to a scratch directory
 * first — together with any loaded {@code Outer$Inner} classes, which CFR resolves by looking
 * beside the analysed file.
 */
public final class DecompilerService {

  public static final String CFR_VERSION = "0.152";
  public static final String NAME = "cfr-" + CFR_VERSION;

  private static final int CACHE_ENTRIES = 64;

  private final Map<String, Cached> cache =
      Collections.synchronizedMap(
          new LinkedHashMap<String, Cached>(16, 0.75f, true) {
            @Override
            protected boolean removeEldestEntry(Map.Entry<String, Cached> eldest) {
              return size() > CACHE_ENTRIES;
            }
          });

  /** What to decompile. Inner classes and a single method name are optional. */
  public static final class Request {
    private final String className;
    private final String digest;
    private final byte[] bytes;
    private final Map<String, byte[]> innerClasses = new LinkedHashMap<>();
    private String methodName;

    public Request(String className, String digest, byte[] bytes) {
      if (className == null || className.isEmpty()) {
        throw new IllegalArgumentException("class is required");
      }
      if (bytes == null || bytes.length == 0) {
        throw new IllegalArgumentException("no class bytes supplied for " + className);
      }
      this.className = className;
      this.digest = digest;
      this.bytes = bytes;
    }

    public Request withInnerClass(String name, byte[] classBytes) {
      innerClasses.put(name, classBytes);
      return this;
    }

    public Request withMethodName(String name) {
      this.methodName = name == null || name.trim().isEmpty() ? null : name.trim();
      return this;
    }

    String cacheKey() {
      return digest + "#" + (methodName == null ? "" : methodName);
    }
  }

  /** Decompiled source plus the method index and line map built from CFR's line-number mapping. */
  public static final class Result {
    private final String source;
    private final List<Map<String, Object>> methods;
    private final int[] sourceLineNumbers;
    private final Map<Integer, Integer> lineRows;
    private final long durationMs;
    private final boolean cached;

    Result(Cached decompiled, long durationMs, boolean cached) {
      this.source = decompiled.source;
      this.methods = decompiled.methods;
      this.sourceLineNumbers = decompiled.sourceLineNumbers;
      this.lineRows = decompiled.lineRows;
      this.durationMs = durationMs;
      this.cached = cached;
    }

    public String getSource() {
      return source;
    }

    public List<Map<String, Object>> getMethods() {
      return methods;
    }

    /** See {@link #sourceLineNumbers}; a copy, since the cache holds the original. */
    public int[] getSourceLineNumbers() {
      return sourceLineNumbers.clone();
    }

    /** See {@link #lineRows}; unmodifiable, since the cache holds the original. */
    public Map<Integer, Integer> getLineRows() {
      return lineRows;
    }

    public long getDurationMs() {
      return durationMs;
    }

    public boolean isCached() {
      return cached;
    }
  }

  /** A decompile that failed; the servlet maps this to 502 with the message. */
  public static final class DecompileException extends RuntimeException {
    DecompileException(String message, Throwable cause) {
      super(message, cause);
    }
  }

  private static final class Cached {
    final String source;
    final List<Map<String, Object>> methods;
    final int[] sourceLineNumbers;
    final Map<Integer, Integer> lineRows;

    Cached(
        String source,
        List<Map<String, Object>> methods,
        int[] sourceLineNumbers,
        Map<Integer, Integer> lineRows) {
      this.source = source;
      this.methods = methods;
      this.sourceLineNumbers = sourceLineNumbers;
      this.lineRows = Collections.unmodifiableMap(lineRows);
    }
  }

  public Result decompile(Request request) {
    long started = System.nanoTime();
    Cached hit = cache.get(request.cacheKey());
    if (hit != null) {
      return new Result(hit, elapsedMs(started), true);
    }
    Capture capture;
    try {
      capture = analyse(request);
    } catch (Throwable t) {
      // CFR is a third party parsing hostile bytecode; nothing it throws —
      // including Errors from a malformed constant pool — may reach the container.
      throw new DecompileException("decompile of " + request.className + " failed: " + t, t);
    }
    String source = capture.source.toString();
    if (source.trim().isEmpty()) {
      throw new DecompileException(
          "decompile of "
              + request.className
              + " produced no source"
              + (capture.problems.isEmpty() ? "" : ": " + String.join("; ", capture.problems)),
          null);
    }
    Cached decompiled =
        new Cached(
            source,
            methodIndex(capture.mappings, source),
            sourceLineNumbers(capture.mappings, source),
            lineRows(capture.mappings, source));
    cache.put(request.cacheKey(), decompiled);
    return new Result(decompiled, elapsedMs(started), false);
  }

  private Capture analyse(Request request) throws IOException {
    Path scratch = Files.createTempDirectory("telltale-diag-cfr");
    try {
      Path target = scratch.resolve(classFileName(request.className));
      Files.write(target, request.bytes);
      for (Map.Entry<String, byte[]> inner : request.innerClasses.entrySet()) {
        Files.write(scratch.resolve(classFileName(inner.getKey())), inner.getValue());
      }
      Capture capture = new Capture();
      CfrDriver driver =
          new CfrDriver.Builder()
              .withOptions(options(request.methodName))
              .withOutputSink(capture)
              .build();
      driver.analyse(Collections.singletonList(target.toAbsolutePath().toString()));
      return capture;
    } finally {
      deleteRecursively(scratch);
    }
  }

  private static Map<String, String> options(String methodName) {
    Map<String, String> options = new LinkedHashMap<>();
    options.put("showversion", "false");
    // Arthas keeps unicode literals verbatim rather than re-escaping them.
    options.put("hideutf", "false");
    // Emits the LINENUMBER sink the method index is built from.
    options.put("trackbytecodeloc", "true");
    if (methodName != null) {
      options.put("methodname", methodName);
    }
    return options;
  }

  static String classFileName(String binaryName) {
    return binaryName.substring(binaryName.lastIndexOf('.') + 1) + ".class";
  }

  /**
   * Contract §3 {@code methods[]}. CFR emits one mapping per method, keyed by <em>bytecode
   * offset</em> with the emitted output line as the value ( {@code getClassFileMappings} is the
   * same keys against the original source lines). The lowest value is therefore the first line of
   * that method's body in {@code source}, 1-based, and the highest is the last line of it that
   * carries an instruction.
   *
   * <p>Both ends are reported because neither can be recovered afterwards. A caller wanting to show
   * one method out of a large class has to derive the end from the <em>next</em> method's start,
   * and that is wrong here: CFR emits a mapping per method including synthetics, so {@code
   * lambda$foo$0}'s lowest line lands <em>inside</em> {@code foo} and this list is not monotonic in
   * the way that heuristic assumes.
   *
   * <p>{@code endLine} is the last mapped line, not the closing brace — the brace carries no
   * instruction — so a reader expanding this to a syntactic block still has to walk out to it.
   */
  static List<Map<String, Object>> methodIndex(
      List<SinkReturns.LineNumberMapping> mappings, String source) {
    int lines = source.split("\n", -1).length;
    List<Map<String, Object>> methods = new ArrayList<>();
    for (SinkReturns.LineNumberMapping mapping : mappings) {
      NavigableMap<Integer, Integer> outputLines = mapping.getMappings();
      if (outputLines == null || outputLines.isEmpty()) {
        continue;
      }
      int line = Collections.min(outputLines.values());
      if (line < 1 || line > lines) {
        continue;
      }
      Map<String, Object> row = new LinkedHashMap<>();
      row.put("name", mapping.methodName());
      row.put("descriptor", mapping.methodDescriptor());
      row.put("line", line);
      row.put("endLine", Math.min(Collections.max(outputLines.values()), lines));
      methods.add(row);
    }
    methods.sort(Comparator.comparingInt(row -> (Integer) row.get("line")));
    return methods;
  }

  /**
   * The original source line behind each decompiled row: entry {@code i} is row {@code i + 1}, and
   * 0 where no instruction maps there — a brace, a declaration, a class compiled without a line
   * table.
   *
   * <p>CFR keys both of its maps by bytecode offset: {@code getMappings} gives the decompiled row
   * an offset landed on, {@code getClassFileMappings} the line the class file's line table gives
   * that offset. Joining them on the offset is what lets a line from the line table — a call site a
   * probe reported — find its row in the source a reader is actually looking at. An offset missing
   * from the line table takes the entry at or before it, which is how the line table itself is
   * meant to be read. A row several lines compiled into keeps the earliest, the first statement the
   * reader sees.
   */
  static int[] sourceLineNumbers(List<SinkReturns.LineNumberMapping> mappings, String source) {
    int[] lines = new int[source.split("\n", -1).length];
    for (SinkReturns.LineNumberMapping mapping : mappings) {
      NavigableMap<Integer, Integer> rows = mapping.getMappings();
      NavigableMap<Integer, Integer> original = mapping.getClassFileMappings();
      if (rows == null || original == null || original.isEmpty()) {
        continue;
      }
      for (Map.Entry<Integer, Integer> offset : rows.entrySet()) {
        int row = offset.getValue();
        Map.Entry<Integer, Integer> line = original.floorEntry(offset.getKey());
        if (line == null || row < 1 || row > lines.length || line.getValue() < 1) {
          continue;
        }
        int index = row - 1;
        if (lines[index] == 0 || line.getValue() < lines[index]) {
          lines[index] = line.getValue();
        }
      }
    }
    return lines;
  }

  /**
   * The decompiled row, 1-based, each original source line landed on — the first, when CFR spread
   * one line over several rows.
   *
   * <p>{@link #sourceLineNumbers} cannot say this. A row several lines were folded into is labelled
   * with the earliest alone, so a later line folded into it looks unmapped, and a reader walking up
   * to the nearest labelled line lands on whatever row sits between: the wrong statement.
   */
  static Map<Integer, Integer> lineRows(
      List<SinkReturns.LineNumberMapping> mappings, String source) {
    int lines = source.split("\n", -1).length;
    Map<Integer, Integer> rows = new TreeMap<>();
    for (SinkReturns.LineNumberMapping mapping : mappings) {
      NavigableMap<Integer, Integer> decompiled = mapping.getMappings();
      NavigableMap<Integer, Integer> original = mapping.getClassFileMappings();
      if (decompiled == null || original == null || original.isEmpty()) {
        continue;
      }
      for (Map.Entry<Integer, Integer> offset : decompiled.entrySet()) {
        int row = offset.getValue();
        Map.Entry<Integer, Integer> line = original.floorEntry(offset.getKey());
        if (line == null || row < 1 || row > lines || line.getValue() < 1) {
          continue;
        }
        Integer first = rows.get(line.getValue());
        if (first == null || row < first) {
          rows.put(line.getValue(), row);
        }
      }
    }
    return rows;
  }

  private static long elapsedMs(long startedNanos) {
    return (System.nanoTime() - startedNanos) / 1_000_000L;
  }

  private static void deleteRecursively(Path root) {
    try (java.util.stream.Stream<Path> paths = Files.walk(root)) {
      List<Path> ordered = new ArrayList<>();
      paths.forEach(ordered::add);
      Collections.reverse(ordered);
      for (Path path : ordered) {
        Files.deleteIfExists(path);
      }
    } catch (IOException ignored) {
      // Scratch files live under java.io.tmpdir; a failed cleanup is not worth failing a read.
    }
  }

  /** Collects CFR's output in memory instead of writing files. */
  private static final class Capture implements OutputSinkFactory {
    final StringBuilder source = new StringBuilder();
    final List<SinkReturns.LineNumberMapping> mappings = new ArrayList<>();
    final List<String> problems = new ArrayList<>();

    @Override
    public List<SinkClass> getSupportedSinks(SinkType sinkType, Collection<SinkClass> available) {
      List<SinkClass> preferred;
      switch (sinkType) {
        case JAVA:
          preferred = Arrays.asList(SinkClass.DECOMPILED, SinkClass.STRING);
          break;
        case LINENUMBER:
          preferred = Arrays.asList(SinkClass.LINE_NUMBER_MAPPING, SinkClass.STRING);
          break;
        default:
          preferred = Collections.singletonList(SinkClass.STRING);
          break;
      }
      List<SinkClass> supported = new ArrayList<>();
      for (SinkClass candidate : preferred) {
        if (available.contains(candidate)) {
          supported.add(candidate);
        }
      }
      return supported.isEmpty() ? Collections.singletonList(SinkClass.STRING) : supported;
    }

    @Override
    @SuppressWarnings("unchecked")
    public <T> Sink<T> getSink(SinkType sinkType, SinkClass sinkClass) {
      if (sinkType == SinkType.JAVA && sinkClass == SinkClass.DECOMPILED) {
        return (Sink<T>)
            (Sink<SinkReturns.Decompiled>) decompiled -> source.append(decompiled.getJava());
      }
      if (sinkType == SinkType.JAVA && sinkClass == SinkClass.STRING) {
        return (Sink<T>) (Sink<String>) source::append;
      }
      if (sinkType == SinkType.LINENUMBER && sinkClass == SinkClass.LINE_NUMBER_MAPPING) {
        return (Sink<T>) (Sink<SinkReturns.LineNumberMapping>) mappings::add;
      }
      if (sinkType == SinkType.EXCEPTION) {
        return (Sink<T>) (Sink<Object>) value -> problems.add(String.valueOf(value));
      }
      return value -> {
        // SUMMARY and PROGRESS are noise for an API caller.
      };
    }
  }
}
