package framefixture;

public final class ForeignSourceClient {
  public static int foreignCalls;
  public String request(String value) { return "original:" + value; }
}
