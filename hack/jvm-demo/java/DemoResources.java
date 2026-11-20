final class DemoResources {
  private DemoResources() {}

  static void close(AutoCloseable... resources) throws Exception {
    Exception failure = null;
    for (AutoCloseable resource : resources) {
      try { resource.close(); }
      catch (Exception error) {
        if (failure == null) failure = error;
        else failure.addSuppressed(error);
      }
    }
    if (failure != null) throw failure;
  }
}
