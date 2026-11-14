package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.NavigableMap;
import java.util.TreeMap;
import java.util.stream.Collectors;
import javax.tools.DiagnosticCollector;
import javax.tools.JavaCompiler;
import javax.tools.JavaFileObject;
import javax.tools.StandardJavaFileManager;
import javax.tools.ToolProvider;
import org.benf.cfr.reader.api.SinkReturns;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

/**
 * Exercises the CFR sink capture against a fixture the test compiles itself, so the expected class
 * shape and method set are known independently of any class that happens to be on the test
 * classpath.
 */
final class DecompilerServiceTest {

  private static final String PACKAGE = "com.flanksource.telltale.diag.fixture";
  private static final String SIMPLE_NAME = "Sample";
  private static final String FQCN = PACKAGE + "." + SIMPLE_NAME;
  private static final String INNER_FQCN = FQCN + "$Helper";
  private static final String SCALE = "scale";
  private static final String LABEL = "label";
  private static final String TWICE = "twice";

  private static final String SOURCE =
      String.join(
          "\n",
          "package " + PACKAGE + ";",
          "",
          "public class " + SIMPLE_NAME + " {",
          "    private final int base;",
          "",
          "    public " + SIMPLE_NAME + "(int base) {",
          "        this.base = base;",
          "    }",
          "",
          "    public int " + SCALE + "(int factor) {",
          "        int total = 0;",
          "        for (int i = 0; i < factor; i++) {",
          "            total += this.base;",
          "        }",
          "        return total;",
          "    }",
          "",
          "    public String " + LABEL + "(String prefix) {",
          "        return prefix + \":\" + this.base;",
          "    }",
          "",
          "    static final class Helper {",
          "        int " + TWICE + "(int value) {",
          "            return value * 2;",
          "        }",
          "    }",
          "}",
          "");

  private static byte[] outerBytes;
  private static byte[] innerBytes;

  @BeforeAll
  static void compileFixture(@TempDir Path workspace) throws IOException {
    Path classes = compile(workspace);
    outerBytes =
        Files.readAllBytes(
            classes.resolve(PACKAGE.replace('.', '/')).resolve(SIMPLE_NAME + ".class"));
    innerBytes =
        Files.readAllBytes(
            classes.resolve(PACKAGE.replace('.', '/')).resolve(SIMPLE_NAME + "$Helper.class"));
  }

  private static Path compile(Path workspace) throws IOException {
    Path source =
        workspace.resolve("src").resolve(PACKAGE.replace('.', '/')).resolve(SIMPLE_NAME + ".java");
    Files.createDirectories(source.getParent());
    Files.write(source, SOURCE.getBytes(StandardCharsets.UTF_8));
    Path classes = workspace.resolve("classes");
    Files.createDirectories(classes);

    JavaCompiler compiler = ToolProvider.getSystemJavaCompiler();
    assertNotNull(
        compiler, "these tests must run on a JDK: javax.tools reports no system Java compiler");
    DiagnosticCollector<JavaFileObject> diagnostics = new DiagnosticCollector<>();
    try (StandardJavaFileManager files =
        compiler.getStandardFileManager(diagnostics, null, StandardCharsets.UTF_8)) {
      Iterable<? extends JavaFileObject> units =
          files.getJavaFileObjectsFromFiles(Collections.singletonList(source.toFile()));
      // Java 8 bytecode (major 52) is what CFR 0.152 sees in the application JVM.
      List<String> options = Arrays.asList("--release", "8", "-g", "-d", classes.toString());
      boolean compiled = compiler.getTask(null, files, diagnostics, options, null, units).call();
      assertTrue(compiled, "fixture compilation failed: " + diagnostics.getDiagnostics());
    }
    return classes;
  }

  private static DecompilerService.Request request() {
    return new DecompilerService.Request(FQCN, BytecodeSource.digest(outerBytes), outerBytes)
        .withInnerClass(INNER_FQCN, innerBytes);
  }

  private static List<String> methodNames(DecompilerService.Result result) {
    return result.getMethods().stream()
        .map(method -> String.valueOf(method.get("name")))
        .collect(Collectors.toList());
  }

  private static Map<String, Object> method(DecompilerService.Result result, String name) {
    return result.getMethods().stream()
        .filter(method -> name.equals(method.get("name")))
        .findFirst()
        .orElseThrow(
            () -> new AssertionError("no " + name + " in method index " + methodNames(result)));
  }

  @Test
  void decompilesTheWholeClassWithEveryDeclaredMethod() {
    DecompilerService.Result result = new DecompilerService().decompile(request());

    String source = result.getSource();
    assertTrue(source.contains("class " + SIMPLE_NAME), source);
    assertTrue(source.contains("public int " + SCALE + "(int "), source);
    assertTrue(source.contains("public String " + LABEL + "(String "), source);
    assertFalse(result.isCached());
  }

  @Test
  void restrictsOutputToASingleMethodWhenOneIsRequested() {
    DecompilerService.Result result =
        new DecompilerService().decompile(request().withMethodName(SCALE));

    assertTrue(result.getSource().contains(SCALE), result.getSource());
    assertFalse(result.getSource().contains(LABEL), result.getSource());
    assertEquals(Collections.singletonList(SCALE), methodNames(result));
  }

  @Test
  void indexesEachMethodAtItsOwnOneBasedLineInTheDecompiledSource() {
    DecompilerService.Result result = new DecompilerService().decompile(request());

    assertTrue(
        methodNames(result).containsAll(Arrays.asList(SCALE, LABEL)),
        "method index was " + methodNames(result));
    String[] lines = result.getSource().split("\n", -1);
    for (String name : Arrays.asList(SCALE, LABEL)) {
      Map<String, Object> method = method(result, name);
      int line = (Integer) method.get("line");
      assertTrue(
          line >= 1 && line <= lines.length, name + " line " + line + " is outside the source");
      assertTrue(
          enclosingDeclaration(lines, line).contains(name),
          name + " is not declared at or above line " + line + ": " + lines[line - 1]);
    }
    assertEquals("(I)I", method(result, SCALE).get("descriptor"));
    assertEquals("(Ljava/lang/String;)Ljava/lang/String;", method(result, LABEL).get("descriptor"));
  }

  /**
   * Both ends, because a caller showing one method out of a large class cannot recover the second
   * afterwards: deriving it from the next method's start breaks on the synthetics CFR also maps,
   * whose start lines land inside the method that hosts them.
   */
  @Test
  void indexesTheLastMappedLineOfEachMethodAsWellAsTheFirst() {
    DecompilerService.Result result = new DecompilerService().decompile(request());

    int lines = result.getSource().split("\n", -1).length;
    for (String name : Arrays.asList(SCALE, LABEL)) {
      Map<String, Object> method = method(result, name);
      int start = (Integer) method.get("line");
      int end = (Integer) method.get("endLine");
      assertTrue(end >= start, name + " ends at " + end + " but starts at " + start);
      assertTrue(end <= lines, name + " ends at " + end + ", past the " + lines + "-line source");
    }
  }

  /**
   * A call site's line comes from the class's line table, which counts the ORIGINAL source; the
   * reader is looking at CFR's reconstruction, whose rows are numbered differently.
   * sourceLineNumbers is the bridge: one entry per decompiled row, naming the original line it came
   * from.
   */
  @Test
  void mapsEachDecompiledStatementBackToItsOriginalSourceLine() {
    DecompilerService.Result result = new DecompilerService().decompile(request());

    String[] rows = result.getSource().split("\n", -1);
    int[] original = result.getSourceLineNumbers();
    assertEquals(rows.length, original.length, "one entry per decompiled row");
    assertEquals(
        originalLine("return prefix + \":\" + this.base;"),
        original[rowContaining(rows, "return prefix")]);
    assertEquals(originalLine("return total;"), original[rowContaining(rows, "return total;")]);
    assertEquals(
        originalLine("total += this.base;"), original[rowContaining(rows, "total += this.base;")]);
    assertEquals(
        Integer.valueOf(rowContaining(rows, "return total;") + 1),
        result.getLineRows().get(originalLine("return total;")),
        "and the inverse, line to 1-based row");
  }

  @Test
  void servesTheLineMapFromTheCacheToo() {
    DecompilerService service = new DecompilerService();

    DecompilerService.Result first = service.decompile(request());
    DecompilerService.Result second = service.decompile(request());

    assertTrue(second.isCached());
    assertTrue(Arrays.equals(first.getSourceLineNumbers(), second.getSourceLineNumbers()));
    assertEquals(first.getLineRows(), second.getLineRows());
  }

  /**
   * CFR can fold a later source line into an earlier statement's row while the line between keeps a
   * row of its own. That row is labelled with its earliest line only, so a reader walking up from
   * line 12 to the nearest labelled line would land on line 11's row — the wrong statement. The
   * line table says where 12 went, and the inverse map keeps it.
   */
  @Test
  void mapsALineFoldedIntoAnEarlierRowToThatRow() {
    // Offsets 0 and 8 decompile onto row 5; offset 4 onto row 6. The class
    // file puts them on lines 10, 11 and 12.
    List<SinkReturns.LineNumberMapping> mappings =
        Collections.singletonList(mapping(rows(0, 5, 4, 6, 8, 5), rows(0, 10, 4, 11, 8, 12)));
    String source = String.join("\n", Collections.nCopies(8, "row"));

    int[] labels = DecompilerService.sourceLineNumbers(mappings, source);
    Map<Integer, Integer> lineRows = DecompilerService.lineRows(mappings, source);

    assertEquals(10, labels[4], "row 5 is labelled with the earliest line folded into it");
    assertEquals(11, labels[5]);
    assertEquals(Integer.valueOf(5), lineRows.get(12), "line 12 was folded into row 5, not row 6");
    assertEquals(Integer.valueOf(6), lineRows.get(11));
  }

  /** An offset-keyed map, from alternating offset/value pairs. */
  private static NavigableMap<Integer, Integer> rows(int... pairs) {
    NavigableMap<Integer, Integer> out = new TreeMap<>();
    for (int i = 0; i < pairs.length; i += 2) {
      out.put(pairs[i], pairs[i + 1]);
    }
    return out;
  }

  private static SinkReturns.LineNumberMapping mapping(
      final NavigableMap<Integer, Integer> decompiledRows,
      final NavigableMap<Integer, Integer> originalLines) {
    return new SinkReturns.LineNumberMapping() {
      @Override
      public String methodName() {
        return "process";
      }

      @Override
      public String methodDescriptor() {
        return "()V";
      }

      @Override
      public NavigableMap<Integer, Integer> getMappings() {
        return decompiledRows;
      }

      @Override
      public NavigableMap<Integer, Integer> getClassFileMappings() {
        return originalLines;
      }
    };
  }

  /** The 1-based line of the fixture's own source holding {@code code}. */
  private static int originalLine(String code) {
    String[] lines = SOURCE.split("\n", -1);
    for (int i = 0; i < lines.length; i++) {
      if (lines[i].contains(code)) {
        return i + 1;
      }
    }
    throw new AssertionError("the fixture source has no line containing " + code);
  }

  /** The 0-based decompiled row holding {@code code}. */
  private static int rowContaining(String[] rows, String code) {
    for (int i = 0; i < rows.length; i++) {
      if (rows[i].contains(code)) {
        return i;
      }
    }
    throw new AssertionError(
        "the decompiled source has no row containing " + code + ":\n" + String.join("\n", rows));
  }

  /** CFR maps a method to its first emitted statement; the signature is at or above it. */
  private static String enclosingDeclaration(String[] lines, int line) {
    StringBuilder window = new StringBuilder();
    for (int i = Math.max(0, line - 3); i < line && i < lines.length; i++) {
      window.append(lines[i]).append('\n');
    }
    return window.toString();
  }

  @Test
  void servesARepeatRequestFromTheDigestKeyedCache() {
    DecompilerService service = new DecompilerService();

    DecompilerService.Result first = service.decompile(request());
    DecompilerService.Result second = service.decompile(request());

    assertFalse(first.isCached());
    assertTrue(second.isCached());
    assertEquals(first.getSource(), second.getSource());
    assertEquals(methodNames(first), methodNames(second));
  }

  @Test
  void keysTheCacheOnTheRequestedMethodSoAWholeClassReadIsNotServedForOne() {
    DecompilerService service = new DecompilerService();

    service.decompile(request());
    DecompilerService.Result single = service.decompile(request().withMethodName(LABEL));

    assertFalse(single.isCached());
    assertEquals(Collections.singletonList(LABEL), methodNames(single));
  }

  @Test
  void namesTheTemporaryClassFileAfterTheBinaryNameSoCfrResolvesInnerClasses() {
    assertEquals(SIMPLE_NAME + ".class", DecompilerService.classFileName(FQCN));
    assertEquals(SIMPLE_NAME + "$Helper.class", DecompilerService.classFileName(INNER_FQCN));
  }
}
