package com.flanksource.telltale.diag.fixture;

/**
 * A class the live-instrumentation test rewrites in THIS JVM: one method that hands a destination
 * on. Kept to itself so redefining it cannot disturb the fixtures other tests define into throwaway
 * loaders.
 */
public class RetransformFixture {

  public String send(String destination) {
    return "sent:" + destination;
  }
}
