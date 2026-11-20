package observerfixture.closedframes;

public class Choice {
  public Object choose(boolean unused, long value) {
    Object selected;
    if (unused) selected = new Unused();
    else selected = new Selected(value);
    return selected;
  }
}
