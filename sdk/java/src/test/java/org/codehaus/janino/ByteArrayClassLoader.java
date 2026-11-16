package org.codehaus.janino;

/**
 * Stands in for Janino's real loader, which OIPA creates one of per business rule it compiles at
 * runtime. The rule table in {@code LoaderProfile} keys on the fully-qualified class name, so the
 * merge behaviour can only be exercised by a loader that genuinely carries this name.
 *
 * <p>Janino is not a dependency of this WAR — it lives inside the OIPA application, which this tool
 * inspects from the outside — so nothing on the test classpath collides with this.
 */
public final class ByteArrayClassLoader extends ClassLoader {

  public ByteArrayClassLoader() {
    super(null);
  }
}
