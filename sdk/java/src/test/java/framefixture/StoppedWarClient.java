package framefixture;

public final class StoppedWarClient {
  public Exception choose(boolean input) {
    Exception selected;
    if (input) selected = new java.io.IOException("input");
    else selected = new java.sql.SQLException("database");
    return selected;
  }
}
