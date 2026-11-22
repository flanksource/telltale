package com.flanksource.telltale;

import static org.junit.jupiter.api.Assertions.*;

import com.flanksource.telltale.diag.fixture.RetransformFixture;
import com.flanksource.telltale.diag.hotpatch.ClassIndex;
import com.flanksource.telltale.diag.hotpatch.ClassOperations;
import com.flanksource.telltale.diag.hotpatch.JsonBody;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;

final class MethodCaptureLeaseTest {
  @Test
  void refusesTracingAClassHeldByRedefinitionAndLeavesThePatchInPlace() throws Exception {
    ClassOperations classes = new ClassOperations();
    RetransformFixture fixture = new RetransformFixture();
    try (MethodCapture patch = new MethodCapture(classes, "jvm_redefine", replacement(), "patch")) {
      Map<String, Object> trace = Collections.singletonMap("targets", Collections.singletonList(target()));
      IllegalStateException error =
          assertThrows(IllegalStateException.class, () -> {
            try (MethodCapture ignored = new MethodCapture(classes, "jvm_trace", trace, "trace")) {}
          });
      assertTrue(error.getMessage().contains("source redefinition"), error.toString());
      assertEquals("patched:active", fixture.send("active"));
    }
    assertEquals("sent:restored", fixture.send("restored"));
  }

  @Test
  void aRedefinitionFailsRenewalAndFinishIfItsSourcePatchWasRemoved() throws Exception {
    ClassOperations classes = new ClassOperations();
    RetransformFixture fixture = new RetransformFixture();
    MethodCapture patch = new MethodCapture(classes, "jvm_redefine", replacement(), "patch");
    assertEquals("patched:active", fixture.send("active"));
    classes.restore(new JsonBody(replacement()));
    assertEquals("sent:restored", fixture.send("restored"));
    IllegalStateException lost = assertThrows(IllegalStateException.class, patch::renew);
    assertTrue(lost.getMessage().contains("lost its patch"));
    Exception restore = assertThrows(Exception.class, patch::finish);
    assertTrue(restore.getMessage().contains("nothing to restore"), restore.toString());
  }

  @Test
  void anEarlierRedefinitionCannotRenewOrRestoreItsSuccessor() throws Exception {
    ClassOperations classes = new ClassOperations();
    RetransformFixture fixture = new RetransformFixture();
    MethodCapture earlier = new MethodCapture(classes, "jvm_redefine", replacement(), "earlier");
    classes.restore(new JsonBody(replacement()));
    Map<String, Object> next = replacement();
    next.put("source", "public String send(String destination) { return \"next:\" + destination; }");
    try (MethodCapture successor = new MethodCapture(classes, "jvm_redefine", next, "successor")) {
      assertThrows(IllegalStateException.class, earlier::renew);
      assertThrows(IllegalStateException.class, earlier::finish);
      assertEquals("next:working", fixture.send("working"));
      successor.renew();
    }
    assertEquals("sent:restored", fixture.send("restored"));
  }

  private static Map<String, Object> target() {
    Map<String, Object> target = new LinkedHashMap<>();
    target.put("class", RetransformFixture.class.getName());
    target.put("method", "send");
    target.put("loader", ClassIndex.hash(RetransformFixture.class.getClassLoader()));
    return target;
  }

  private static Map<String, Object> replacement() {
    Map<String, Object> source = target();
    source.put("source", "public String send(String destination) { return \"patched:\" + destination; }");
    return source;
  }
}
