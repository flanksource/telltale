package com.flanksource.telltale.observer.http;

import observerfixture.springhttp.SpringReactiveFixture;
import observerfixture.springhttp.SpringBlockingFixture;
import org.junit.Test;

public class SpringHttpObserverTest {
  @Test public void observesAnExistingSpring53WebClientStreamingAcrossThreads() throws Exception {
    SpringReactiveFixture.streaming();
  }

  @Test public void preservesSpring53WebClientCancellation() throws Exception {
    SpringReactiveFixture.cancellation();
  }

  @Test public void observesSpring53FunctionalRouting() throws Exception {
    SpringReactiveFixture.functionalStreaming();
  }

  @Test public void observesAnExistingSpring53RestTemplateBean() throws Exception {
    SpringBlockingFixture.restTemplate();
  }

}
