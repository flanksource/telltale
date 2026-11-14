package com.flanksource.telltale.diag.hotpatch;

import java.util.ArrayList;
import java.util.Collections;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import org.objectweb.asm.ClassReader;
import org.objectweb.asm.ClassVisitor;
import org.objectweb.asm.ClassWriter;
import org.objectweb.asm.Label;
import org.objectweb.asm.MethodVisitor;
import org.objectweb.asm.Opcodes;
import org.objectweb.asm.Type;
import org.objectweb.asm.commons.AdviceAdapter;
import org.objectweb.asm.commons.Method;

/**
 * Rewrites one method so it reports what it does, leaving what it does unchanged.
 *
 * <p>This is the half of hot patching that needs neither a compiler nor a decompiler. The original
 * body is <em>wrapped</em>, not replaced: an entry probe, a timed exit probe, optionally the
 * arguments and the returned value, and optionally a catch-all that records a propagating exception
 * and rethrows it unchanged. Nothing about the method's behaviour moves.
 *
 * <p>Three consequences fall out of transforming the live bytes rather than recompiling
 * reconstructed source, and they are the reason this path exists:
 *
 * <ul>
 *   <li>No JDK is needed in the pod — instrumentation never runs javac.
 *   <li>Weaving another agent already applied to the class survives, because every method except
 *       the target is copied verbatim (see {@link TargetLoaderClassWriter}) and the target's own
 *       instructions are kept and merely bracketed.
 *   <li>No method, field or class is added, so HotSpot's "method bodies only" restriction on
 *       redefinition is satisfied by construction. A body containing a lambda or an anonymous class
 *       — which the source path has to refuse — is ordinary here.
 * </ul>
 */
public final class InstrumentService {

  /** Where the injected calls land; see {@code DiagAgent}, on the system class path. */
  static final Type AGENT = Type.getObjectType("com/flanksource/telltale/diag/agent/DiagAgent");

  private static final Type THROWABLE = Type.getObjectType("java/lang/Throwable");

  // The injected code names exactly one class — the agent — and four of its
  // methods. Every additional name would be another way for a redefinition to
  // fail to resolve inside application code, which is the one failure this
  // subsystem must not cause. Timing moved into the agent for the same reason:
  // one fewer injected call, and one fewer class to resolve.
  private static final Method PROBE_ENTER =
      new Method(
          "probeEnter",
          "(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;I"
              + "[Ljava/lang/Object;)J");
  private static final Method PROBE_EXIT = new Method("probeExit", "(Ljava/lang/String;J)V");
  private static final Method PROBE_RETURN =
      new Method("probeReturn", "(Ljava/lang/String;JLjava/lang/Object;)Ljava/lang/Object;");
  private static final Method PROBE_THROW =
      new Method("probeThrow", "(Ljava/lang/String;JLjava/lang/Throwable;)V");

  /**
   * Whether a call site belongs in the tree beneath an instrumented method.
   *
   * <p>Lifted out of the adapter so it can be asserted directly: reached only through a bytecode
   * round-trip, each of these rules could only be tested by finding a class that happens to call
   * the thing being excluded.
   *
   * @param skipJdk leave the platform out, arthas' {@code --skipJDKMethod}
   */
  static boolean tracesCallsTo(String owner, String name, boolean skipJdk) {
    if (owner == null || owner.equals(AGENT.getInternalName()) || isGenerated(owner, name)) {
      return false;
    }
    return !skipJdk || !isPlatform(owner);
  }

  /**
   * A class the platform owns.
   *
   * <p>A superset of arthas' {@code java/}: the same argument covers the rest of the platform's own
   * namespaces, and application's stack reaches all of them.
   */
  private static boolean isPlatform(String owner) {
    return owner.startsWith("java/")
        || owner.startsWith("javax/")
        || owner.startsWith("jdk/")
        || owner.startsWith("sun/")
        || owner.startsWith("com/sun/");
  }

  /**
   * A class some framework generated rather than someone wrote.
   *
   * <p>The CGLIB markers are arthas' {@code AsmUtils.isEnhancerByCGLIB}; {@code FastClass} is the
   * dispatch half of the same mechanism, {@code $$Lambda} is its {@code ClassUtils.isLambdaClass},
   * and {@code com/sun/proxy/} is the JDK dynamic proxy that {@link #isPlatform} would only catch
   * while the platform is being skipped.
   *
   * <p>application weaves its aspects at COMPILE time rather than proxying at runtime, so the CGLIB
   * markers never fire here and the weaver's own artifacts do instead: {@code
   * ActivityBll$AjcClosure1}, the {@code ajc$} methods the weaver renames the original body to, and
   * {@code aspectOf()}, the generated singleton accessor every advised call site invokes first.
   *
   * <p>Measured, not assumed. The first live capture of {@code ActivityBll.process} with calls on
   * spent 83 of its 100 events on this plumbing — closure constructors and {@code aspectOf} alone
   * were a third of it — leaving 17 for the method anyone was actually looking at.
   *
   * <p>The advice bodies themselves are NOT excluded. An aspect is code someone wrote and it does
   * real work; what is dropped here is only the machinery that dispatches to it.
   *
   * <p>Unconditional, the way arthas keeps its box types unconditional: this is noise whether or
   * not the platform is being reported, because it describes the interception rather than the call.
   */
  private static boolean isGenerated(String owner, String name) {
    return owner.contains("$$EnhancerBySpringCGLIB$$")
        || owner.contains("$$EnhancerByCGLIB$$")
        || owner.contains("$$FastClassBySpringCGLIB$$")
        || owner.contains("$$FastClassByCGLIB$$")
        || owner.contains("$$Lambda")
        || owner.contains("$AjcClosure")
        || owner.startsWith("com/sun/proxy/")
        || owner.startsWith("org/aspectj/")
        || isWeaverPlumbing(name);
  }

  /** A method the AspectJ weaver generated rather than one anyone declared. */
  private static boolean isWeaverPlumbing(String name) {
    return name != null
        && (name.startsWith("ajc$") || "aspectOf".equals(name) || "hasAspect".equals(name));
  }

  /** What to instrument, and what it should report. */
  public static final class Request {
    private final String className;
    private final byte[] bytecode;
    private final ClassLoader loader;
    private final MethodSelector selector;
    private final String probeKey;
    private boolean captureArgs;
    private boolean captureReturn;
    private boolean captureException;
    private boolean captureCalls;

    /**
     * Leave the platform's own call sites out of the call tree.
     *
     * <p>True by default, matching arthas' {@code --skipJDKMethod true}. It only reads at all when
     * {@link #withCalls} is on.
     */
    private boolean skipJdk = true;

    /**
     * @param bytecode the bytes the class is <em>currently running</em> (see {@link
     *     BytecodeSource}), never the bytes on disk.
     * @param loader the loader that defined the class, used to compute frames.
     */
    public Request(
        String className,
        byte[] bytecode,
        ClassLoader loader,
        MethodSelector selector,
        String probeKey) {
      if (className == null || className.trim().isEmpty()) {
        throw new IllegalArgumentException("class is required");
      }
      if (bytecode == null || bytecode.length == 0) {
        throw new IllegalArgumentException("no bytecode to instrument for " + className);
      }
      if (selector == null) {
        throw new IllegalArgumentException(
            "a method selector is required to instrument " + className);
      }
      validateProbeKey(probeKey);
      this.className = className.trim();
      this.bytecode = bytecode;
      this.loader = loader;
      this.selector = selector;
      this.probeKey = probeKey.trim();
    }

    public Request withArgs(boolean value) {
      this.captureArgs = value;
      return this;
    }

    public Request withReturn(boolean value) {
      this.captureReturn = value;
      return this;
    }

    /**
     * Report what the instrumented method CALLS, as nested frames.
     *
     * <p>Off by default, and deliberately: a method's call sites outnumber the method itself by an
     * order of magnitude, and turning this on for a hot one is how a ring fills with events nobody
     * asked for. It is what `arthas trace` gives and `watch` does not, and the flag is the line
     * between the two.
     */
    public Request withCalls(boolean value) {
      this.captureCalls = value;
      return this;
    }

    public Request withException(boolean value) {
      this.captureException = value;
      return this;
    }

    /**
     * Report the platform's own calls in the call tree too.
     *
     * <p>The inverse of arthas' {@code --skipJDKMethod}, phrased so the default is the quiet one.
     * Worth turning on to see what a method spends inside a JDK collection or a JDBC driver; expect
     * the tree to grow by an order of magnitude when you do.
     */
    public Request withPlatformCalls(boolean value) {
      this.skipJdk = !value;
      return this;
    }
  }

  /** The rewritten class, and every method the selector named. */
  public static final class Result {
    private final List<MethodSelector.Match> matched;
    private final byte[] bytecode;

    Result(List<MethodSelector.Match> matched, byte[] bytecode) {
      this.matched = Collections.unmodifiableList(matched);
      this.bytecode = bytecode;
    }

    /**
     * Every method the selector named, armed or not, in declaration order.
     *
     * <p>The whole SET including the ones that could not be armed, because the resolution is the
     * answer worth checking: a pattern that named four and armed three is indistinguishable from
     * one that named three if only the successes come back, and that is precisely the "captured
     * less than you asked for" reading everything here exists to prevent.
     */
    public List<MethodSelector.Match> getMatched() {
      return matched;
    }

    /** The armed methods' JVM descriptors, in declaration order. */
    public List<String> getDescriptors() {
      List<String> descriptors = new ArrayList<String>(matched.size());
      for (MethodSelector.Match match : matched) {
        if (match.isInstrumentable()) {
          descriptors.add(match.getMethod().getDescriptor());
        }
      }
      return descriptors;
    }

    /**
     * The one armed method's descriptor.
     *
     * @throws IllegalStateException when a pattern armed several — a caller that can only hold one
     *     must not silently receive the first of four.
     */
    public String getDescriptor() {
      List<String> descriptors = getDescriptors();
      if (descriptors.size() != 1) {
        throw new IllegalStateException(
            "this selector armed " + descriptors.size() + " methods; ask for getDescriptors()");
      }
      return descriptors.get(0);
    }

    public byte[] getBytecode() {
      return bytecode;
    }
  }

  /**
   * Probe keys are embedded in the injected code and name a buffer the client reads back, so they
   * are constrained to the shape the client sends: {@code Class.method.suffix}. The client checks
   * this too; the client is not the gate.
   */
  static void validateProbeKey(String key) {
    if (key == null || key.trim().isEmpty()) {
      throw new IllegalArgumentException("a probe key is required");
    }
    String trimmed = key.trim();
    for (int i = 0; i < trimmed.length(); i++) {
      char c = trimmed.charAt(i);
      boolean allowed =
          c == '.'
              || c == '-'
              || c == '_'
              || c == '$'
              || (c >= 'a' && c <= 'z')
              || (c >= 'A' && c <= 'Z')
              || (c >= '0' && c <= '9');
      if (!allowed) {
        throw new IllegalArgumentException(
            "probe key \""
                + key
                + "\" contains '"
                + c
                + "'; keys are letters, digits, '.', '-', '_' and '$' (shaped"
                + " Class.method.suffix)");
      }
    }
  }

  /**
   * Rewrites every method the selector names, in ONE pass over the class.
   *
   * <p>One pass because a class is redefined whole. Arming a pattern's methods one at a time would
   * mean re-reading the already-rewritten bytes for the second, and its probe calls would end up
   * wrapping the first's — nested frames on one invocation, and durations that contain each other.
   */
  public Result instrument(Request request) {
    ClassReader reader = new ClassReader(request.bytecode);
    final List<MethodSelector.Match> matched =
        request.selector.resolveAll(request.className, reader);
    // Keyed on name+descriptor because a name alone is not an identity here:
    // a pattern may arm two overloads and the visitor sees them separately.
    final Set<String> armed = new HashSet<String>();
    for (MethodSelector.Match match : matched) {
      if (match.isInstrumentable()) {
        armed.add(match.getMethod().getName() + match.getMethod().getDescriptor());
      }
    }
    ClassWriter writer = new TargetLoaderClassWriter(reader, request.loader);

    try {
      // Frames are computed as the instructions are visited, so the
      // resolution this can fail on happens here, not in toByteArray.
      reader.accept(
          new ClassVisitor(Opcodes.ASM9, writer) {
            @Override
            public MethodVisitor visitMethod(
                int access, String name, String descriptor, String signature, String[] exceptions) {
              MethodVisitor delegate =
                  super.visitMethod(access, name, descriptor, signature, exceptions);
              if (!armed.contains(name + descriptor)) {
                // Returned unwrapped, so ASM copies this method's bytes
                // verbatim rather than re-deriving them.
                return delegate;
              }
              if ((access & (Opcodes.ACC_ABSTRACT | Opcodes.ACC_NATIVE)) != 0) {
                throw new IllegalArgumentException(
                    request.className
                        + "."
                        + name
                        + descriptor
                        + " is "
                        + ((access & Opcodes.ACC_NATIVE) != 0 ? "native" : "abstract")
                        + " and has no body to instrument");
              }
              return new ProbeAdapter(delegate, access, name, descriptor, request);
            }
          },
          ClassReader.SKIP_FRAMES);
      return new Result(matched, writer.toByteArray());
    } catch (TypeNotPresentException e) {
      // COMPUTE_FRAMES resolves types at merge points through the target's
      // loader. One it cannot see is a real answer — "this method's frames
      // cannot be computed here" — not something to paper over with
      // java/lang/Object, which would produce bytes the verifier rejects.
      throw new IllegalStateException(
          "computing stack map frames for "
              + request.className
              + "."
              + describe(armed)
              + " needs "
              + e.typeName()
              + ", which its own class loader cannot resolve",
          e);
    }
  }

  private static String describe(Set<String> armed) {
    StringBuilder out = new StringBuilder();
    for (String method : armed) {
      if (out.length() > 0) {
        out.append(", ");
      }
      out.append(method);
    }
    return out.toString();
  }

  /**
   * Brackets one method's existing instructions with probe calls.
   *
   * <p>{@link AdviceAdapter} is what makes the constructor case correct: its {@code onMethodEnter}
   * fires <em>after</em> the {@code super()} call, so the entry probe and the catch-all's protected
   * range never see an uninitialised {@code this} — which the verifier would reject.
   */
  private static final class ProbeAdapter extends AdviceAdapter {

    private final String probeKey;
    // Pushed as constants, so the injected code needs no getClass() and no
    // `this` — which is why one emission works for instance, static and
    // constructor alike.
    private final String className;
    private final String methodName;

    /**
     * The method's own JVM descriptor, pushed alongside its name.
     *
     * <p>A constant here rather than a lookup at the far end: a name is not an identity, and once
     * one probe key covers a set of methods the events are the only place the overload survives.
     * The instrument response carries a descriptor too, but it answers for the one call that armed
     * this method and cannot say which of a set a given entry belongs to.
     */
    private final String methodDescriptor;

    private final boolean captureArgs;
    private final boolean captureReturn;
    private final boolean captureException;
    private final boolean captureCalls;
    private final boolean skipJdk;
    private final Type returnType;
    private final Label bodyStart = new Label();
    private final Label bodyEnd = new Label();

    private int frameSlot = -1;
    private int returnSlot = -1;
    private int throwableSlot = -1;

    /**
     * Holds the token of the call currently being made.
     *
     * <p>ONE slot is enough for a whole method because call sites cannot overlap. In {@code a(b())}
     * the JVM has already invoked and closed {@code b} before it invokes {@code a} — the probe for
     * {@code a} is emitted immediately before its own invocation instruction, which is after {@code
     * b}'s closing probe. Nesting between them comes from the agent's per-thread stack, not from a
     * second local.
     */
    private int callSlot = -1;

    /**
     * True while this adapter is emitting its own probe calls.
     *
     * <p>{@code invokeStatic} routes through {@link #visitMethodInsn}, so without this the wrapper
     * would wrap itself: the guard is what stops the emitter recursing into the code it is
     * emitting.
     */
    private boolean emitting;

    /**
     * The source line of the instructions being visited, from the method's line table; 0 until the
     * first entry, and throughout a class compiled without one.
     *
     * <p>Pushed with each call site, because it is the one thing that lets a trace show the code
     * that made a call: the line table is gone once the class is running, and a decompiled class
     * has lines of its own.
     */
    private int line;

    ProbeAdapter(
        MethodVisitor delegate, int access, String name, String descriptor, Request request) {
      super(Opcodes.ASM9, delegate, access, name, descriptor);
      this.probeKey = request.probeKey;
      this.className = request.className;
      this.methodName = name;
      this.methodDescriptor = descriptor;
      this.captureArgs = request.captureArgs;
      this.captureReturn = request.captureReturn;
      this.captureException = request.captureException;
      this.captureCalls = request.captureCalls;
      this.skipJdk = request.skipJdk;
      this.returnType = Type.getReturnType(descriptor);
    }

    /**
     * Brackets one invocation the instrumented method makes.
     *
     * <p>The stack is why this is safe rather than delicate. At an invocation instruction the
     * receiver and arguments are already pushed; pushing six more values and calling a static that
     * pops exactly those six leaves everything beneath untouched, because the operand stack is a
     * stack. The returned token is stored to a local, so nothing of ours is left on it when the
     * real invocation runs.
     *
     * <p>The callee is reported by its DECLARED owner at the call site, not its runtime class — an
     * interface call reports the interface. That is the same thing arthas trace reports and it is
     * what the bytecode knows; the alternative is a receiver type lookup on every call, inside the
     * method being measured.
     *
     * <p>Nothing is emitted around a throw path: if the callee throws, its closing probe is skipped
     * and the agent's own {@code popTo} unwinds the dangling frame when the enclosing method's
     * handler reports. Wrapping every call site in its own try/catch would multiply the method's
     * handler table by its call count to report what the enclosing frame already makes visible.
     */
    @Override
    public void visitLineNumber(int number, Label start) {
      line = number;
      super.visitLineNumber(number, start);
    }

    @Override
    public void visitMethodInsn(
        int opcode, String owner, String name, String descriptor, boolean isInterface) {
      if (!captureCalls || emitting || !worthTracing(owner, name)) {
        super.visitMethodInsn(opcode, owner, name, descriptor, isInterface);
        return;
      }
      emitting = true;
      push(probeKey);
      push(owner.replace('/', '.'));
      push(name);
      push(descriptor);
      push(line);
      visitInsn(Opcodes.ACONST_NULL);
      invokeStatic(AGENT, PROBE_ENTER);
      storeLocal(callSlot);
      emitting = false;

      super.visitMethodInsn(opcode, owner, name, descriptor, isInterface);

      emitting = true;
      push(probeKey);
      loadLocal(callSlot);
      invokeStatic(AGENT, PROBE_EXIT);
      emitting = false;
    }

    /**
     * Whether a call site is one a reader asked about.
     *
     * <p>Ported from arthas, which draws these lines in {@code Enhancer.enhance} and defaults them
     * the same way. Three separate reasons to leave a call site alone:
     *
     * <ul>
     *   <li><b>The platform</b>, when {@code skipJdk} (the default, and arthas' {@code
     *       --skipJDKMethod true}). {@code "a" + b()} is a StringBuilder construction and three
     *       appends, and a hot method's platform calls outnumber its own by an order of magnitude —
     *       reporting them buries the one call that matters and evicts it from the ring to make
     *       room. Set {@code platform} to keep them.
     *   <li><b>Generated proxy and aspect frames</b>, always. A Spring AOP or CGLIB proxy sits
     *       between the caller and the method that does the work, so tracing it reports the
     *       interception rather than the call; the same goes for a lambda's synthetic class, whose
     *       name is a counter nobody wrote and which changes between runs. Unconditional because
     *       they are noise whether or not the platform is being reported, which is how arthas
     *       treats the box types.
     *   <li><b>The agent</b>, always, because instrumenting our own probes is unbounded recursion
     *       inside application code. {@link #emitting} already covers the calls this adapter emits;
     *       this covers a method that happens to call the agent itself.
     * </ul>
     */
    private boolean worthTracing(String owner, String name) {
      return tracesCallsTo(owner, name, skipJdk);
    }

    /**
     * Opens the frame.
     *
     * <p>The frame token goes into a local rather than staying on the operand stack because
     * entering an exception handler wipes the stack down to the throwable — anything parked there
     * would be unreachable from the handler that has to close the frame. {@code newLocal} is called
     * here and not in the constructor, and the token is only ever touched through {@code
     * loadLocal}/{@code storeLocal}, because both are what {@link
     * org.objectweb.asm.commons.LocalVariablesSorter} requires to renumber the method's own locals
     * correctly.
     *
     * <p>The store must land before {@code bodyStart}: the handler's stack map frame has to prove
     * the local is definitely assigned, and a range that opens first cannot.
     */
    @Override
    protected void onMethodEnter() {
      frameSlot = newLocal(Type.LONG_TYPE);
      if (captureReturn && returnType.getSort() != Type.VOID) {
        returnSlot = newLocal(returnType);
      }
      throwableSlot = newLocal(THROWABLE);
      if (captureCalls) {
        callSlot = newLocal(Type.LONG_TYPE);
      }

      push(probeKey);
      push(className);
      push(methodName);
      push(methodDescriptor);
      // A method's own entry has no call site in view: its caller was not
      // rewritten, so there is no line to report.
      push(0);
      if (captureArgs) {
        loadArgArray();
      } else {
        visitInsn(Opcodes.ACONST_NULL);
      }
      invokeStatic(AGENT, PROBE_ENTER);
      storeLocal(frameSlot);
      mark(bodyStart);
    }

    /**
     * Emits the exit frame at every normal return.
     *
     * <p>An {@code ATHROW} is deliberately left alone. When exceptions are being captured the
     * catch-all below reports it once, with the elapsed time; when they are not, a method that
     * threw produces an entry frame and no exit frame, which reads as what happened — entered,
     * never returned — rather than as a normal exit that never occurred.
     */
    @Override
    protected void onMethodExit(int opcode) {
      if (opcode == ATHROW) {
        return;
      }
      if (returnSlot >= 0) {
        // Through a local rather than stack acrobatics: dup/swap depends
        // on whether the returned value occupies one slot or two, and the
        // boxing probeReturn needs would have to differ with it.
        storeLocal(returnSlot);
        push(probeKey);
        loadLocal(frameSlot);
        loadLocal(returnSlot);
        box(returnType);
        invokeStatic(AGENT, PROBE_RETURN);
        pop();
        loadLocal(returnSlot);
      }
      push(probeKey);
      loadLocal(frameSlot);
      invokeStatic(AGENT, PROBE_EXIT);
    }

    /**
     * Closes the frame on the way out of a propagating exception, and rethrows it unchanged.
     *
     * <p>The handler is emitted ALWAYS, not only when the caller asked for exception detail. With
     * depth tracked per thread, an entry with no matching close leaves the depth permanently raised
     * on a pooled Tomcat thread, so every later trace on that thread would report nesting that
     * never happened — off by however many exceptions it had swallowed. {@code captureException}
     * therefore controls how much is said about the throwable, never whether the frame is closed.
     * The elapsed time is reported either way, which is the case worth having: a method that took
     * eight seconds and then threw.
     *
     * <p>{@code catchException} runs here, after the method's own handlers were visited, so ASM
     * writes this entry last and every handler the method declared for itself still wins. It must
     * stay before {@code super.visitMaxs}.
     */
    @Override
    public void visitMaxs(int maxStack, int maxLocals) {
      mark(bodyEnd);
      catchException(bodyStart, bodyEnd, null);
      storeLocal(throwableSlot);
      push(probeKey);
      loadLocal(frameSlot);
      loadLocal(throwableSlot);
      invokeStatic(AGENT, PROBE_THROW);
      loadLocal(throwableSlot);
      visitInsn(Opcodes.ATHROW);
      // COMPUTE_FRAMES recomputes both, but the visitor contract still
      // requires the call that ends the method.
      super.visitMaxs(maxStack, maxLocals);
    }
  }
}
