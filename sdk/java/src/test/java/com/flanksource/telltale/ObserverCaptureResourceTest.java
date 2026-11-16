package com.flanksource.telltale;

import static org.junit.Assert.*;
import com.flanksource.telltale.observer.ObserverRuntime;
import io.opentelemetry.proto.common.v1.KeyValue;
import io.opentelemetry.proto.profiles.v1development.ProfilesData;
import java.lang.management.ManagementFactory;
import java.util.*;
import javax.management.ObjectName;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.Test;

public class ObserverCaptureResourceTest {
  @Test public void transportsTheConfiguredResourceThroughTheSharedMBeanIntoNativeProfiles() throws Exception {
    java.lang.instrument.Instrumentation instrumentation = ByteBuddyAgent.install();
    com.flanksource.telltale.diag.agent.DiagAgent.agentmain("", instrumentation);
    ObserverRuntime.install(instrumentation);
    Map<String, Object> config = new LinkedHashMap<>();
    config.put("service", "checkout-fixture");
    config.put("resource", Collections.singletonMap("deployment.environment.name", "integration"));
    RuntimeClient client = new RuntimeClient(config);
    try (Capture capture = client.capture("http_top", Collections.emptyMap(), "resource-fixture", false)) {
      ProfilesData profiles = ProfilesData.parseFrom(capture.dump());
      Map<String, Object> attributes = new LinkedHashMap<>();
      for (KeyValue value : profiles.getResourceProfiles(0).getResource().getAttributesList())
        attributes.put(value.getKey(), value.getValue().getStringValue());
      Map<String, Object> expected = new LinkedHashMap<>();
      expected.put("service.name", "checkout-fixture");
      expected.put("deployment.environment.name", "integration");
      expected.put("process.pid", ManagementFactory.getRuntimeMXBean().getName().split("@")[0]);
      expected.put("telltale.source.instance", ObserverRuntime.SOURCE);
      assertEquals(expected, attributes);
    } finally {
      ManagementFactory.getPlatformMBeanServer().unregisterMBean(new ObjectName(RuntimeService.NAME));
    }
  }
}
