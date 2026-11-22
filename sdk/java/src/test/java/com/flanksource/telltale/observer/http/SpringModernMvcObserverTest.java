package com.flanksource.telltale.observer.http;

import observerfixture.SpringVersionFixture;
import observerfixture.springhttp.SpringMvcFixture;
import org.junit.Test;

public class SpringModernMvcObserverTest {
  @Test public void observesSpring62JakartaMvcDeferredResult() throws Exception {
    isolated("telltale.http.spring6Mvc", "6.2.", "deferred");
  }

  @Test public void observesSpring62JakartaMvcCallableRedispatch() throws Exception {
    isolated("telltale.http.spring6Mvc", "6.2.", "callable");
  }

  @Test public void observesSpring62JakartaMvcForward() throws Exception {
    isolated("telltale.http.spring6Mvc", "6.2.", "forward");
  }

  @Test public void observesSpring70JakartaMvcDeferredResult() throws Exception {
    isolated("telltale.http.spring7Mvc", "7.0.", "deferred");
  }

  @Test public void observesSpring70JakartaMvcCallableRedispatch() throws Exception {
    isolated("telltale.http.spring7Mvc", "7.0.", "callable");
  }

  @Test public void observesSpring70JakartaMvcForward() throws Exception {
    isolated("telltale.http.spring7Mvc", "7.0.", "forward");
  }

  private static void isolated(String property, String version, String method) throws Exception {
    SpringVersionFixture.run(property, version, SpringMvcFixture.class.getName(), method);
  }
}
