package framefixture;

public final class ClosedLoaderClient {
  public static Base choose(boolean first) {
    Base selected;
    if (first) selected = new First();
    else selected = new Second();
    return selected;
  }
  public static class Base {}
  public static final class First extends Base {}
  public static final class Second extends Base {}
}
