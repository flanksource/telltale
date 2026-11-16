package com.flanksource.telltale;

import static org.junit.Assert.*;
import java.lang.reflect.InvocationTargetException;
import org.junit.Test;

public class SessionFailureTest {
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
