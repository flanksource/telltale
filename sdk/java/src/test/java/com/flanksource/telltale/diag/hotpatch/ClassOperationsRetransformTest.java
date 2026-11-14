package com.flanksource.telltale.diag.hotpatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;

import com.flanksource.telltale.diag.agent.DiagAgent;
import com.flanksource.telltale.diag.fixture.Probes;
import com.flanksource.telltale.diag.fixture.RetransformFixture;
import java.lang.instrument.ClassFileTransformer;
import java.lang.instrument.Instrumentation;
import java.nio.charset.StandardCharsets;
import java.security.ProtectionDomain;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.Map;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

/**
 * An instrumented class must keep recording when something else retransforms it.
 *
 * <p>Once a retransform-capable agent has changed a class, HotSpot caches the class file as it was
 * before that change and starts every later retransformation from the cache — not from the bytes a
 * later {@code redefineClasses} installed. A probe armed by redefinition is therefore dropped by
 * the next retransformation from any source (an APM agent, an Arthas {@code watch}, this agent's
 * own live bytecode read): the ring stays open, the lease stays held, and nothing records.
 *
 * <p>Runs against the real {@link Instrumentation} the test JVM was started with ({@code
 * -javaagent}, see build.gradle), because the failure lives entirely in how the JVM treats bytes.
 */
final class ClassOperationsRetransformTest {

  private static final String KEY = "RetransformFixture.send.retransform";
  private static final String DESTINATION = "Cycle.SubmitTask";

  /** What every injected probe call names, so its absence means the class is clean. */
  private static final String AGENT_INTERNAL_NAME = "com/flanksource/telltale/diag/agent/DiagAgent";

  private final ClassOperations classes = new ClassOperations();

  @AfterEach
  void putTheClassBack() throws Exception {
    if (!DiagAgent.probeKeysOwnedBy(owner()).isEmpty() || DiagAgent.ORIGINAL_BYTES.containsKey(owner())) {
      classes.restore(new JsonBody(target()));
    }
    DiagAgent.closeAllProbes();
  }

  @Test
  void keepsRecordingAfterTheInstrumentedClassIsRetransformed() throws Exception {
    Instrumentation instrumentation = instrumentation();
    RetransformFixture fixture = new RetransformFixture();
    transformedByAnotherAgent(instrumentation);
    classes.instrument(new JsonBody(instrumentRequest()));

    fixture.send(DESTINATION);
    assertEquals(Arrays.asList("enter", "exit"), Probes.kinds(KEY));

    new BytecodeSource(instrumentation).bytecodeOf(RetransformFixture.class);

    fixture.send(DESTINATION);
    assertEquals(
        Arrays.asList("enter", "exit", "enter", "exit"),
        Probes.kinds(KEY),
        "a retransformation of the class must not silently disarm the probe");
  }

  @Test
  void leavesNoProbeInTheClassAfterTheSpanRestoresIt() throws Exception {
    Map<String, Object> restore = target();
    restore.put("probe", Collections.singletonMap("key", KEY));
    assertRestoreLeavesNoProbe(restore);
  }

  @Test
  void leavesNoProbeInTheClassAfterAnOperatorRestore() throws Exception {
    assertRestoreLeavesNoProbe(target());
  }

  private void assertRestoreLeavesNoProbe(Map<String, Object> restore) throws Exception {
    Instrumentation instrumentation = instrumentation();
    transformedByAnotherAgent(instrumentation);
    classes.instrument(new JsonBody(instrumentRequest()));

    classes.restore(new JsonBody(restore));
    byte[] live = new BytecodeSource(instrumentation).bytecodeOf(RetransformFixture.class);

    assertFalse(
        new String(live, StandardCharsets.ISO_8859_1).contains(AGENT_INTERNAL_NAME),
        "the restored class must not call the agent, even after it is retransformed");
    assertEquals(new ArrayList<String>(), DiagAgent.probeKeysOwnedBy(owner()));
  }

  private static Instrumentation instrumentation() {
    Instrumentation instrumentation = DiagAgent.INSTRUMENTATION;
    assertNotNull(instrumentation, "the test JVM must run with the diag agent as -javaagent");
    return instrumentation;
  }

  /**
   * What an APM agent or Arthas leaves behind on a class it has touched: a retransform-capable
   * transformer changed its bytes once, which is what makes HotSpot cache the pre-change class file.
   */
  private static void transformedByAnotherAgent(Instrumentation instrumentation) throws Exception {
    ClassFileTransformer other =
        new ClassFileTransformer() {
          @Override
          public byte[] transform(
              ClassLoader loader,
              String className,
              Class<?> redefined,
              ProtectionDomain domain,
              byte[] buffer) {
            return redefined == RetransformFixture.class ? buffer.clone() : null;
          }
        };
    instrumentation.addTransformer(other, true);
    try {
      instrumentation.retransformClasses(RetransformFixture.class);
    } finally {
      instrumentation.removeTransformer(other);
    }
  }

  private static Map<String, Object> instrumentRequest() {
    Map<String, Object> probe = new LinkedHashMap<>();
    probe.put("key", KEY);
    probe.put("args", Boolean.TRUE);
    Map<String, Object> body = target();
    body.put("method", "send");
    // No lease: nothing in a unit test should leave a reaper restoring classes behind the next
    // test's back.
    body.put("leaseSeconds", -1);
    body.put("probe", probe);
    return body;
  }

  private static Map<String, Object> target() {
    Map<String, Object> body = new LinkedHashMap<>();
    body.put("class", RetransformFixture.class.getName());
    body.put("loader", ClassIndex.hash(RetransformFixture.class.getClassLoader()));
    return body;
  }

  private static String owner() {
    return ClassIndex.hash(RetransformFixture.class.getClassLoader())
        + "|"
        + RetransformFixture.class.getName();
  }
}
