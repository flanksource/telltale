package com.flanksource.telltale.auth;

/** An authentication (401) or authorization (403) refusal; the message never contains the token. */
public final class AuthException extends Exception {
  private final int status;

  AuthException(int status, String reason) {
    super(reason);
    this.status = status;
  }

  AuthException(int status, String reason, Throwable cause) {
    super(reason, cause);
    this.status = status;
  }

  public int status() {
    return status;
  }

  static AuthException unauthorized(String reason) {
    return new AuthException(401, reason);
  }

  static AuthException forbidden(String reason) {
    return new AuthException(403, reason);
  }
}
