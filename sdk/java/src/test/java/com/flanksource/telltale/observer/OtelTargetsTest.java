package com.flanksource.telltale.observer;

import static org.junit.Assert.*;
import java.util.*;
import org.junit.Test;

public class OtelTargetsTest {
  @Test public void exposesPinnedAbisAndExplicitProviderCoverageFromTheTargetCatalog() {
    Map<String, Object> catalog = OtelTargets.catalog();
    Map<?, ?> pins = (Map<?, ?>) catalog.get("certifiedPins");
    assertEquals("2.33.1", pins.get("instrumentation.version"));
    assertEquals("1.67.0", pins.get("sdk.version"));
    assertEquals("1.11.1-alpha", pins.get("profiles.version"));
    Map<?, ?> jms = (Map<?, ?>) ((Map<?, ?>) catalog.get("adapters")).get("jms");
    assertTrue(jms.get("certifiedProvider").toString().contains("Artemis 2.44.0"));
    assertTrue(jms.get("opaqueProviders").toString().contains("Listener/FIFO guesses are forbidden"));
    assertTrue(catalog.get("compatibility").toString().contains("not complete provider coverage"));
  }
}
