package com.flanksource.telltale.auth;

import com.flanksource.telltale.Scope;
import java.util.*;

/** A verified caller: the JWT subject and the scopes it was granted. */
public final class Principal {
  private final String subject;
  private final Set<String> scopes;

  Principal(String subject, Set<String> scopes) {
    this.subject = subject;
    this.scopes = Collections.unmodifiableSet(new LinkedHashSet<>(scopes));
  }

  public String subject() {
    return subject;
  }

  public Set<String> scopes() {
    return scopes;
  }

  public void require(Scope scope) throws AuthException {
    if (!scopes.contains(scope.wire()))
      throw AuthException.forbidden("token lacks required scope " + scope.wire());
  }
}
