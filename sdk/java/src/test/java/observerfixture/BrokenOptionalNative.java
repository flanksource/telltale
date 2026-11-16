package observerfixture;

public final class BrokenOptionalNative {
  static { initialize(); }

  private static void initialize() {
    throw new UnsatisfiedLinkError("optional native fixture is unavailable");
  }

  public static native void poll();
}
