package com.flanksource.telltale;

import static org.junit.Assert.*;
import java.lang.reflect.InvocationTargetException;
import org.junit.Test;

public class SessionFailureTest {
  @Test public void interruptedShutdownStillWaitsForSessionWorkers() throws Exception {
    Session session = new Session(null, null, false);
    java.lang.reflect.Field field = Session.class.getDeclaredField("calls");
    field.setAccessible(true);
    java.util.concurrent.ExecutorService calls = (java.util.concurrent.ExecutorService) field.get(session);
    java.util.concurrent.CountDownLatch running = new java.util.concurrent.CountDownLatch(1);
    java.util.concurrent.CountDownLatch stopped = new java.util.concurrent.CountDownLatch(1);
    calls.execute(() -> {
      running.countDown();
      try { new java.util.concurrent.CountDownLatch(1).await(); }
      catch (InterruptedException expected) { Thread.currentThread().interrupt(); }
      finally { stopped.countDown(); }
    });
    assertTrue(running.await(5, java.util.concurrent.TimeUnit.SECONDS));
    Thread.currentThread().interrupt();
    try {
      session.close();
      assertTrue(session.stopped());
      assertEquals(0, stopped.getCount());
      assertTrue(Thread.currentThread().isInterrupted());
    } finally { Thread.interrupted(); }
  }

  @Test public void preservesTheJVMVerificationFailureBehindReflectiveCaptureConstruction() {
    VerifyError verification = new VerifyError("Invalid stack map in example.MessageHandler.onMessage");
    assertEquals(verification.toString(), Session.failureMessage(new InvocationTargetException(verification)));
  }

  @Test public void unwrapsNestedReflectiveFailuresAndPreservesOrdinaryErrors() {
    IllegalArgumentException invalid = new IllegalArgumentException("destinations must be an array");
    assertEquals(invalid.toString(), Session.failureMessage(invalid));
    assertEquals(invalid.toString(), Session.failureMessage(new InvocationTargetException(new InvocationTargetException(invalid))));
  }
}
