package observerfixture;

public final class ExistingClient {
  public String request(String value) {
    if (value.equals("fail")) throw new IllegalArgumentException("original failure");
    return "original:" + value;
  }
}
