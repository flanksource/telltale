package com.flanksource.telltale.observer.http;

import observerfixture.springhttp.SpringMvcFixture;
import org.junit.Test;

public class SpringMvcObserverTest {
  @Test public void observesAnExistingMvcAsyncControllerUntilContainerCompletion() throws Exception {
    SpringMvcFixture.deferred();
  }

  @Test public void observesAnExistingMvcCallableAcrossAsyncRedispatch() throws Exception {
    SpringMvcFixture.callable();
  }

  @Test public void recordsAForwardAsOneIncomingExchange() throws Exception {
    SpringMvcFixture.forward();
  }

}
