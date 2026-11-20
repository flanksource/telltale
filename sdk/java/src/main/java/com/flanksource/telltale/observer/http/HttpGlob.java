package com.flanksource.telltale.observer.http;

import java.util.regex.Pattern;

final class HttpGlob {
  private HttpGlob() {}

  static Pattern compile(String glob) {
    if (glob.isEmpty()) throw invalid(glob);
    StringBuilder expression = new StringBuilder("\\A");
    int[] cursor = {0};
    while (cursor[0] < glob.length()) {
      int value = glob.codePointAt(cursor[0]); cursor[0] += Character.charCount(value);
      if (value == '*') {
        while (cursor[0] < glob.length() && glob.charAt(cursor[0]) == '*') cursor[0]++;
        expression.append("[^/]*");
      } else if (value == '?') expression.append("[^/]");
      else if (value == '[') expression.append(characterClass(glob, cursor));
      else {
        if (value == '\\') {
          if (cursor[0] == glob.length()) throw invalid(glob);
          value = glob.codePointAt(cursor[0]); cursor[0] += Character.charCount(value);
        }
        expression.append(Pattern.quote(new String(Character.toChars(value))));
      }
    }
    return Pattern.compile(expression.append("\\z").toString());
  }

  private static String characterClass(String glob, int[] cursor) {
    boolean negated = cursor[0] < glob.length() && glob.charAt(cursor[0]) == '^';
    if (negated) cursor[0]++;
    StringBuilder ranges = new StringBuilder();
    int count = 0;
    while (true) {
      if (cursor[0] < glob.length() && glob.charAt(cursor[0]) == ']' && count > 0) { cursor[0]++; break; }
      int low = escaped(glob, cursor), high = low;
      if (cursor[0] < glob.length() && glob.charAt(cursor[0]) == '-') { cursor[0]++; high = escaped(glob, cursor); }
      if (low <= high) {
        ranges.append("\\x{").append(Integer.toHexString(low)).append('}');
        if (low != high) ranges.append("-\\x{").append(Integer.toHexString(high)).append('}');
      }
      count++;
    }
    if (ranges.length() == 0) return negated ? "(?s:.)" : "(?!)";
    return "[" + (negated ? "^" : "") + ranges + "]";
  }

  private static int escaped(String glob, int[] cursor) {
    if (cursor[0] == glob.length() || glob.charAt(cursor[0]) == '-' || glob.charAt(cursor[0]) == ']') throw invalid(glob);
    if (glob.charAt(cursor[0]) == '\\' && ++cursor[0] == glob.length()) throw invalid(glob);
    int value = glob.codePointAt(cursor[0]); cursor[0] += Character.charCount(value);
    if (cursor[0] == glob.length()) throw invalid(glob);
    return value;
  }

  private static IllegalArgumentException invalid(String glob) { return new IllegalArgumentException("invalid HTTP selection glob: " + glob); }
}
