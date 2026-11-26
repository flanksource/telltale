package com.flanksource.telltale.observer.http;

import observerfixture.SpringVersionFixture;
import observerfixture.springhttp.SpringReactiveFixture;
import observerfixture.springhttp.SpringBlockingFixture;
import org.junit.Test;

public class SpringModernHttpObserverTest {
  @Test public void observesAnExistingSpring62WebClientStreamingAcrossThreads() throws Exception {
    isolated("telltale.http.spring6", "6.2.", "streaming");
  }

  @Test public void preservesSpring62WebClientCancellation() throws Exception {
    isolated("telltale.http.spring6", "6.2.", "cancellation");
  }

  @Test public void observesSpring62FunctionalRouting() throws Exception {
    isolated("telltale.http.spring6", "6.2.", "functionalStreaming");
  }

  @Test public void observesAnExistingSpring62RestClientBean() throws Exception {
    isolatedBlocking("telltale.http.spring6", "6.2.", "restClient");
  }

  @Test public void observesAnExistingSpring62HttpInterfaceBean() throws Exception {
    isolatedBlocking("telltale.http.spring6", "6.2.", "httpInterface");
  }

  @Test public void observesAnExistingSpring70WebClientStreamingAcrossThreads() throws Exception {
    isolated("telltale.http.spring7", "7.0.", "streaming");
  }

  @Test public void preservesSpring70WebClientCancellation() throws Exception {
    isolated("telltale.http.spring7", "7.0.", "cancellation");
  }

  @Test public void observesSpring70FunctionalRouting() throws Exception {
    isolated("telltale.http.spring7", "7.0.", "functionalStreaming");
  }

  @Test public void observesAnExistingSpring70RestClientBean() throws Exception {
    isolatedBlocking("telltale.http.spring7", "7.0.", "restClient");
  }

  @Test public void observesAnExistingSpring70HttpInterfaceBean() throws Exception {
    isolatedBlocking("telltale.http.spring7", "7.0.", "httpInterface");
  }

  private static void isolated(String property, String version, String method) throws Exception {
    SpringVersionFixture.run(property, version, SpringReactiveFixture.class.getName(), method);
  }

  private static void isolatedBlocking(String property, String version, String method) throws Exception {
    SpringVersionFixture.run(property, version, SpringBlockingFixture.class.getName(), method);
  }
}
