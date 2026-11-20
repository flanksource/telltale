package com.flanksource.telltale.diag.hotpatch;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.objectweb.asm.ClassReader;
import org.objectweb.asm.commons.Method;

/**
 * Replaces one method of a live class with a freshly compiled body: generate a stand-in for the
 * class from its own shape, compile the author's method against it, and splice only that method's
 * bytecode into the bytes the class is running.
 *
 * <p>This exists so that replacing a method needs no decompiler. Decompiling the whole class and
 * recompiling it is unusable in the environment this ships into for two independent reasons: a
 * reconstruction of a class an agent has already woven frequently does not compile at all, and even
 * when it does, recompiling it discards that agent's instrumentation from every other method — on
 * the one class someone is actively debugging.
 */
public final class MethodPatchService {

  private final SkeletonService skeletons = new SkeletonService();
  private final CompilerService compiler = new CompilerService();
  private final MethodSplicer splicer = new MethodSplicer();

  /** A compile of one replacement method, positioned in the source the author sent. */
  public static final class Result {
    private final CompilerService.Result compiled;
    private final List<Map<String, Object>> diagnostics;
    private final Method target;
    private final byte[] liveBytecode;

    Result(
        CompilerService.Result compiled,
        List<Map<String, Object>> diagnostics,
        Method target,
        byte[] liveBytecode) {
      this.compiled = compiled;
      this.diagnostics = diagnostics;
      this.target = target;
      this.liveBytecode = liveBytecode;
    }

    public boolean isSuccess() {
      return compiled.isSuccess();
    }

    /** The resolved JVM descriptor of the method being replaced. */
    public String getDescriptor() {
      return target.getDescriptor();
    }

    /**
     * The compile response, with every diagnostic repositioned onto the method the caller actually
     * submitted.
     */
    public Map<String, Object> toMap() {
      Map<String, Object> out = new LinkedHashMap<String, Object>(compiled.toMap());
      out.put("diagnostics", diagnostics);
      return out;
    }
  }

  /**
   * Compiles {@code methodSource} against a stand-in for {@code target}'s live shape, without
   * touching the running class.
   *
   * @param liveBytecode what the class is currently running (see {@link BytecodeSource}).
   */
  public Result compile(
      String className,
      Class<?> target,
      byte[] liveBytecode,
      MethodSelector selector,
      String methodSource,
      String classpath) {
    Method resolved = selector.resolve(className, new ClassReader(liveBytecode));
    SkeletonService.Skeleton skeleton = skeletons.skeleton(target, methodSource, resolved);
    CompilerService.Result compiled =
        compiler.compile(
            new CompilerService.Request(className, skeleton.getSource(), target)
                .withClasspath(classpath));
    return new Result(
        compiled,
        rebase(compiled.getMessages(), skeleton.getMethodLine(), className),
        resolved,
        liveBytecode);
  }

  /**
   * Splices the compiled method into the live bytes.
   *
   * @return the class bytes to redefine with.
   */
  public byte[] splice(String className, Result result, ClassLoader loader) {
    if (!result.isSuccess()) {
      throw new IllegalStateException(
          className + " did not compile, so there is nothing to splice");
    }
    return splicer.splice(
        className, result.liveBytecode, result.compiled.getClasses(), result.target, loader);
  }

  /**
   * Moves javac's line numbers from the generated stand-in onto the method the caller sent, since
   * that is the only text the caller has to look at.
   *
   * <p>A diagnostic that lands in the generated part is not renumbered into something that looks
   * like the author's code — it is stripped of its position and labelled, because a wrong line
   * number is read as a fact.
   */
  private static List<Map<String, Object>> rebase(
      List<CompilerService.Message> messages, int methodLine, String className) {
    List<Map<String, Object>> out = new ArrayList<Map<String, Object>>();
    for (CompilerService.Message message : messages) {
      Map<String, Object> row = message.toMap();
      Object reported = row.get("line");
      long line = reported instanceof Number ? ((Number) reported).longValue() : -1L;
      if (line >= methodLine) {
        row.put("line", line - methodLine + 1);
      } else {
        row.put("line", -1L);
        row.put("column", -1L);
        row.put(
            "message", "in the generated stand-in for " + className + ": " + row.get("message"));
      }
      out.add(row);
    }
    return out;
  }
}
