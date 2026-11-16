package com.flanksource.telltale.diag.jmx;

import static org.junit.Assert.*;

import java.util.*;
import javax.management.*;
import org.junit.Test;

public class NotificationHubTest {
  public interface EmitterMBean {
    void emit();
  }

  public static class Emitter extends NotificationBroadcasterSupport implements EmitterMBean {
    private long sequence;

    public void emit() {
      sendNotification(new Notification("example.event", this, ++sequence));
    }
  }

  @Test
  public void sharesListenersAndReportsCursorLoss() throws Exception {
    MBeanServer server = MBeanServerFactory.createMBeanServer();
    NotificationHub hub = new NotificationHub(server);
    try {
      Emitter first = new Emitter(), second = new Emitter();
      server.registerMBean(first, new ObjectName("example:type=Emitter,name=a"));
      server.registerMBean(second, new ObjectName("example:type=Emitter,name=b"));
      String small =
          hub.register("example:type=Emitter,*", Collections.singletonList("example."), 2);
      String large = hub.register("example:type=Emitter,*", Collections.emptyList(), 10);
      first.emit();
      second.emit();
      first.emit();
      Map<String, Object> batch = hub.read(small, 0, 10);
      assertEquals(1L, batch.get("dropped"));
      assertEquals(3L, batch.get("nextSeq"));
      assertEquals(2, ((List<?>) batch.get("events")).size());
      hub.freeze(small);
      second.emit();
      assertEquals(3L, hub.read(small, 0, 10).get("writeSeq"));
      hub.deregister(small);
      first.emit();
      assertEquals(5, ((List<?>) hub.read(large, 0, 10).get("events")).size());
      hub.deregister(large);
      assertEquals(Collections.emptyList(), hub.list());
    } finally {
      hub.shutdown();
      MBeanServerFactory.releaseMBeanServer(server);
    }
  }
}
