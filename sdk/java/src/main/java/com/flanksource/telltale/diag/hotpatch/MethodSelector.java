package com.flanksource.telltale.diag.hotpatch;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.NoSuchElementException;
import org.objectweb.asm.ClassReader;
import org.objectweb.asm.ClassVisitor;
import org.objectweb.asm.MethodVisitor;
import org.objectweb.asm.Opcodes;
import org.objectweb.asm.Type;
import org.objectweb.asm.commons.Method;

/**
 * A reference to one method of a class, in whichever form the author wrote it.
 *
 * <p>A JVM addresses a method by an exact descriptor — {@code
 * (Lexample/application/pas/dcl/ActivityFilterDcl;Ljava/util/List;Ljava/lang/String;)Ljava/lang/String;}
 * — while a person writes a name, a name with parameter types, or a signature pasted out of a stack
 * trace. Four forms are accepted:
 *
 * <pre>
 *   buildQuery                                        bare name, any arity
 *   buildQuery(ActivityFilterDcl, List, String)       simple parameter types
 *   private static String buildQuery(Foo f, Bar b)    a pasted Java signature
 *   buildQuery(Ljava/util/List;)Ljava/lang/String;    a JVM descriptor
 * </pre>
 *
 * <p>{@link #resolve} is what makes this the gate rather than a convenience. The client parses the
 * same forms before making the request, but only the WAR holds the loaded class, so only the WAR
 * can enumerate the overloads and refuse an ambiguous one. Guessing an overload here would
 * instrument or patch a method the author did not name and report success either way — the
 * wrong-overload analogue of patching a class under the wrong loader.
 */
public final class MethodSelector {

  /**
   * The most methods one pattern may arm.
   *
   * <p>Every instrumented method costs probe calls on a live JVM's hot path and a share of one
   * bounded ring, so a pattern that matched two hundred of them would be a mistake discovered by
   * watching the JVM slow down. The number is named in the refusal so it can be argued with rather
   * than guessed at.
   */
  public static final int MAX_PATTERN_METHODS = 16;

  /** One method a class declares, with the access flags a pattern has to read. */
  public static final class Declared {
    final int access;
    final Method method;

    public Declared(int access, Method method) {
      this.access = access;
      this.method = method;
    }
  }

  /**
   * One method a selector named, and whether it can actually be armed.
   *
   * <p>A skipped match is reported rather than dropped. A pattern that named four methods and armed
   * three is otherwise indistinguishable from one that named three — the caller sees a list of
   * three either way — and "it captured less than you asked for, silently" is the failure this
   * whole subsystem is built to make impossible.
   */
  public static final class Match {
    private final Method method;
    private final String skipped;

    Match(Method method, String skipped) {
      this.method = method;
      this.skipped = skipped;
    }

    public Method getMethod() {
      return method;
    }

    /** False when the method matched by name but has no body to instrument. */
    public boolean isInstrumentable() {
      return skipped == null;
    }

    /** Why this match was not armed, or null when it was. */
    public String getSkipped() {
      return skipped;
    }
  }

  private final String name;

  /** Simple parameter type names, or null when no parameter list was written. */
  private final List<String> params;

  /** An exact JVM descriptor, or null when the author wrote a readable form. */
  private final String descriptor;

  /**
   * The parameter list was written {@code (*)}: this exact name, every arity.
   *
   * <p>A third thing from the two either side of it. A bare name is a request for ONE method and
   * refuses when it is ambiguous; a name glob is a request for everything matching a prefix, which
   * reaches names nobody wrote. Neither says "the overloads of this method", which is what a caller
   * measuring a cost spread across them actually wants.
   */
  private final boolean anyParams;

  /**
   * The name carries a glob, or the parameter list does — either way this selector asks for a SET
   * rather than one method. Derived rather than passed in, so there is no way to build a selector
   * whose flag disagrees with it.
   */
  private final boolean pattern;

  private MethodSelector(String name, List<String> params, String descriptor) {
    this.name = name;
    this.params = params;
    this.descriptor = descriptor;
    this.anyParams = params != null && params.size() == 1 && "*".equals(params.get(0));
    this.pattern = this.anyParams || name.indexOf('*') >= 0 || name.indexOf('?') >= 0;
  }

  public static MethodSelector parse(String spec) {
    String trimmed = spec == null ? "" : spec.trim();
    if (trimmed.isEmpty()) {
      throw new IllegalArgumentException("method selector is empty");
    }
    int open = trimmed.indexOf('(');
    if (open < 0) {
      String bare = lastJavaIdentifier(trimmed);
      if (bare.isEmpty()) {
        throw new IllegalArgumentException(
            "method selector \"" + spec + "\" does not name a method");
      }
      return new MethodSelector(bare, null, null);
    }

    String name = lastJavaIdentifier(trimmed.substring(0, open).trim());
    if (name.isEmpty()) {
      throw new IllegalArgumentException(
          "method selector \"" + spec + "\" does not name a method before \"(\"");
    }
    int closing = matchParen(trimmed, open);
    if (closing < 0) {
      throw new IllegalArgumentException(
          "method selector \"" + spec + "\" has an unclosed parameter list");
    }

    // A JVM descriptor is the only form carrying a return type AFTER the ')'.
    // A lone ';' is a pasted signature someone terminated, not a return
    // descriptor — no descriptor return type is a bare semicolon.
    String rest = trimmed.substring(closing + 1).trim();
    if (!rest.isEmpty() && !";".equals(rest)) {
      return new MethodSelector(name, null, trimmed.substring(open, closing + 1) + rest);
    }

    String inner = trimmed.substring(open + 1, closing).trim();
    if (inner.isEmpty()) {
      return new MethodSelector(name, Collections.<String>emptyList(), null);
    }
    List<String> params = new ArrayList<String>();
    for (String part : splitTopLevel(inner)) {
      String typeName = parameterTypeName(part);
      if (typeName.isEmpty()) {
        throw new IllegalArgumentException(
            "method selector \"" + spec + "\" has an unreadable parameter \"" + part.trim() + "\"");
      }
      params.add(typeName);
    }
    return new MethodSelector(name, params, null);
  }

  public String getName() {
    return name;
  }

  /**
   * Reports whether a JVM descriptor satisfies this selector. A selector that named no parameter
   * list matches any arity.
   */
  public boolean matches(String candidate) {
    if (descriptor != null) {
      return descriptor.equals(candidate);
    }
    if (params == null || anyParams) {
      return true;
    }
    Type[] actual;
    try {
      actual = Type.getArgumentTypes(candidate);
    } catch (RuntimeException e) {
      return false;
    }
    if (actual.length != params.size()) {
      return false;
    }
    for (int i = 0; i < actual.length; i++) {
      if (!simpleName(actual[i]).equals(params.get(i))) {
        return false;
      }
    }
    return true;
  }

  /**
   * Picks the one method of {@code reader}'s class that this selector names.
   *
   * @throws NoSuchElementException when nothing matches, listing what the class does declare.
   * @throws IllegalArgumentException when several overloads match, listing their descriptors —
   *     never a silent pick.
   */
  public Method resolve(String className, ClassReader reader) {
    return resolve(className, declaredMethods(declaredIn(reader)));
  }

  /**
   * Every method of {@code reader}'s class this selector names.
   *
   * <p>A pattern resolves to the whole set; anything else resolves to exactly one, with {@link
   * #resolve}'s refusals unchanged. That asymmetry is the point rather than an inconsistency: an
   * author writing a name is naming a method, and quietly instrumenting five overloads they did not
   * ask for is the same defect as quietly instrumenting one of four — doing other than what was
   * written and reporting success either way.
   *
   * @throws NoSuchElementException when nothing matches, listing what the class declares.
   * @throws IllegalArgumentException when a bare name is ambiguous, or a pattern exceeds {@link
   *     #MAX_PATTERN_METHODS}.
   */
  public List<Match> resolveAll(String className, ClassReader reader) {
    return resolveAll(className, declaredIn(reader));
  }

  List<Match> resolveAll(String className, List<Declared> declared) {
    if (!pattern) {
      return Collections.singletonList(
          new Match(resolve(className, declaredMethods(declared)), null));
    }
    List<Match> matched = new ArrayList<Match>();
    int armed = 0;
    for (Declared candidate : declared) {
      if (!reachable(candidate)
          || !globMatches(name, candidate.method.getName())
          || !matches(candidate.method.getDescriptor())) {
        continue;
      }
      String skipped = noBodyReason(candidate.access);
      matched.add(new Match(candidate.method, skipped));
      if (skipped == null) {
        armed++;
      }
    }
    if (matched.isEmpty()) {
      throw new NoSuchElementException(
          className
              + " has no method matching "
              + this
              + "; it declares "
              + names(declaredMethods(declared)));
    }
    if (armed == 0) {
      throw new NoSuchElementException(
          className
              + " has nothing to instrument matching "
              + this
              + "; every method it names is "
              + matched.get(0).getSkipped()
              + " ("
              + descriptors(matchedMethods(matched))
              + ")");
    }
    if (armed > MAX_PATTERN_METHODS) {
      throw new IllegalArgumentException(
          this
              + " matches "
              + armed
              + " methods of "
              + className
              + ", and instrumenting them all would put probe calls on that many"
              + " hot paths at once; at most "
              + MAX_PATTERN_METHODS
              + " may be armed together."
              + " Narrow the pattern: "
              + descriptors(matchedMethods(matched)));
    }
    return matched;
  }

  /**
   * Whether a pattern may reach this method at all.
   *
   * <p>{@code <clinit>} is the one whose failure is an {@code ExceptionInInitializerError} that
   * breaks its class for the life of the JVM, and {@code <init>} is not something anyone writes a
   * pattern to reach. Synthetic and bridge methods — {@code lambda$...} bodies, covariant bridges —
   * were emitted by the compiler rather than written by anyone, so a trace full of them is noise
   * nobody would recognise.
   *
   * <p>These are excluded from the match rather than reported as skipped: a pattern was never
   * asking for them, so listing them as things it could not arm would be noise of a different kind.
   */
  private static boolean reachable(Declared candidate) {
    if (candidate.method.getName().startsWith("<")) {
      return false;
    }
    return (candidate.access & (Opcodes.ACC_SYNTHETIC | Opcodes.ACC_BRIDGE)) == 0;
  }

  /**
   * Why a matched method has no body to wrap, or null when it has one.
   *
   * <p>Reported rather than dropped, because a pattern over an interface's implementations reaches
   * abstract declarations routinely, and the caller has to be able to tell "you named four, three
   * were armed, the fourth is abstract" from "you named three".
   */
  private static String noBodyReason(int access) {
    if ((access & Opcodes.ACC_NATIVE) != 0) {
      return "native";
    }
    if ((access & Opcodes.ACC_ABSTRACT) != 0) {
      return "abstract";
    }
    return null;
  }

  private static List<Method> matchedMethods(List<Match> matched) {
    List<Method> out = new ArrayList<Method>(matched.size());
    for (Match match : matched) {
      out.add(match.getMethod());
    }
    return out;
  }

  /**
   * Glob matching over a method name: {@code *} spans any run of characters, {@code ?} exactly one.
   *
   * <p>Iterative with a backtrack point rather than recursive, so a pattern of several stars cannot
   * cost exponential time on a name that nearly matches.
   */
  static boolean globMatches(String pattern, String candidate) {
    int p = 0;
    int c = 0;
    int star = -1;
    int afterStar = 0;
    while (c < candidate.length()) {
      if (p < pattern.length()
          && (pattern.charAt(p) == '?' || pattern.charAt(p) == candidate.charAt(c))) {
        p++;
        c++;
      } else if (p < pattern.length() && pattern.charAt(p) == '*') {
        star = p++;
        afterStar = c;
      } else if (star >= 0) {
        // Give the last star one more character and resume after it.
        p = star + 1;
        c = ++afterStar;
      } else {
        return false;
      }
    }
    while (p < pattern.length() && pattern.charAt(p) == '*') {
      p++;
    }
    return p == pattern.length();
  }

  private static List<Declared> declaredIn(ClassReader reader) {
    final List<Declared> declared = new ArrayList<Declared>();
    reader.accept(
        new ClassVisitor(Opcodes.ASM9) {
          @Override
          public MethodVisitor visitMethod(
              int access,
              String methodName,
              String methodDescriptor,
              String signature,
              String[] exceptions) {
            declared.add(new Declared(access, new Method(methodName, methodDescriptor)));
            return null;
          }
        },
        ClassReader.SKIP_CODE | ClassReader.SKIP_DEBUG | ClassReader.SKIP_FRAMES);
    return declared;
  }

  private static List<Method> declaredMethods(List<Declared> declared) {
    List<Method> methods = new ArrayList<Method>(declared.size());
    for (Declared candidate : declared) {
      methods.add(candidate.method);
    }
    return methods;
  }

  Method resolve(String className, List<Method> declared) {
    List<Method> sameName = new ArrayList<Method>();
    List<Method> matched = new ArrayList<Method>();
    for (Method candidate : declared) {
      if (!candidate.getName().equals(name)) {
        continue;
      }
      sameName.add(candidate);
      if (matches(candidate.getDescriptor())) {
        matched.add(candidate);
      }
    }
    if (matched.size() == 1) {
      return matched.get(0);
    }
    if (matched.isEmpty()) {
      if (sameName.isEmpty()) {
        throw new NoSuchElementException(
            className + " declares no method named " + name + "; it declares " + names(declared));
      }
      throw new NoSuchElementException(
          className
              + " has no "
              + name
              + " matching "
              + this
              + "; its overloads are "
              + descriptors(sameName));
    }
    throw new IllegalArgumentException(
        this
            + " matches "
            + matched.size()
            + " overloads of "
            + className
            + ", and instrumenting the wrong one would capture nothing without saying so."
            + " Name one exactly, as a descriptor: "
            + descriptors(matched));
  }

  /** Renders the selector the way it would be written, for messages and labels. */
  @Override
  public String toString() {
    if (descriptor != null) {
      return name + descriptor;
    }
    if (params == null) {
      return name;
    }
    StringBuilder out = new StringBuilder(name).append('(');
    for (int i = 0; i < params.size(); i++) {
      if (i > 0) {
        out.append(", ");
      }
      out.append(params.get(i));
    }
    return out.append(')').toString();
  }

  /**
   * The simple type name a descriptor argument reduces to: {@code List}, not {@code
   * java.util.List}; {@code Entry}, not {@code Map$Entry}; {@code String[]} for both arrays and
   * varargs. That is what a reader writes, and it is unambiguous enough to pick an overload without
   * either side having to translate into the other's dialect.
   */
  private static String simpleName(Type type) {
    String rendered = type.getClassName();
    int bracket = rendered.indexOf('[');
    String base = bracket < 0 ? rendered : rendered.substring(0, bracket);
    String suffix = bracket < 0 ? "" : rendered.substring(bracket);
    int cut = Math.max(base.lastIndexOf('.'), base.lastIndexOf('$'));
    return base.substring(cut + 1) + suffix;
  }

  /**
   * Reduces one declared parameter to its simple type name, discarding annotations, a {@code final}
   * modifier, generic arguments and the parameter's own name, and folding varargs into an array.
   */
  private static String parameterTypeName(String part) {
    String text = part.trim();
    while (text.startsWith("@")) {
      int end = 1;
      while (end < text.length() && isJavaIdentifier(text.charAt(end))) {
        end++;
      }
      if (end < text.length() && text.charAt(end) == '(') {
        int closing = matchParen(text, end);
        if (closing >= 0) {
          end = closing + 1;
        }
      }
      text = text.substring(end).trim();
    }
    if (text.startsWith("final ")) {
      text = text.substring("final ".length()).trim();
    }
    if (text.isEmpty()) {
      return "";
    }

    // Split off a trailing parameter name: the last top-level whitespace run
    // that is not inside <>. "List<String> ids" -> "List<String>".
    int space = lastTopLevelSpace(text);
    if (space >= 0) {
      text = text.substring(0, space).trim();
    }

    StringBuilder array = new StringBuilder();
    if (text.endsWith("...")) {
      text = text.substring(0, text.length() - 3).trim();
      array.append("[]");
    }
    while (text.endsWith("]")) {
      int open = text.lastIndexOf('[');
      if (open < 0) {
        break;
      }
      text = text.substring(0, open).trim();
      array.append("[]");
    }
    int generic = text.indexOf('<');
    if (generic >= 0) {
      text = text.substring(0, generic).trim();
    }
    int dot = text.lastIndexOf('.');
    if (dot >= 0) {
      text = text.substring(dot + 1);
    }
    return text.isEmpty() ? "" : text + array;
  }

  /**
   * The trailing method name of {@code s}, which for a pasted signature is the method name —
   * everything before it being modifiers, type parameters and the return type. {@code <init>} and
   * {@code <clinit>} keep their brackets so a constructor can be named at all.
   *
   * <p>Glob characters count as part of the name, and that is load-bearing rather than incidental.
   * Without it the leading walk-back over non-name characters swallows the trailing {@code *} and
   * {@code describe*} parses cleanly to {@code describe} — one method armed while the caller
   * believes it named a set, which nothing downstream can detect. Neither character appears next to
   * a method name in any signature someone would paste: a generic wildcard's {@code ?} lives inside
   * the parameter list or the return type, never adjacent to the name.
   */
  private static String lastJavaIdentifier(String s) {
    int end = s.length();
    while (end > 0 && !isMethodNameChar(s.charAt(end - 1))) {
      end--;
    }
    int start = end;
    while (start > 0 && isMethodNameChar(s.charAt(start - 1))) {
      start--;
    }
    String identifier = s.substring(start, end);
    if (start > 0 && s.charAt(start - 1) == '<' && end < s.length() && s.charAt(end) == '>') {
      return "<" + identifier + ">";
    }
    return identifier;
  }

  private static boolean isMethodNameChar(char c) {
    return c == '*' || c == '?' || isJavaIdentifier(c);
  }

  private static boolean isJavaIdentifier(char c) {
    return c == '_'
        || c == '$'
        || (c >= 'a' && c <= 'z')
        || (c >= 'A' && c <= 'Z')
        || (c >= '0' && c <= '9');
  }

  /** The index of the ')' closing the '(' at {@code open}, or -1 when unbalanced. */
  private static int matchParen(String s, int open) {
    int depth = 0;
    for (int i = open; i < s.length(); i++) {
      if (s.charAt(i) == '(') {
        depth++;
      } else if (s.charAt(i) == ')' && --depth == 0) {
        return i;
      }
    }
    return -1;
  }

  /** Splits on ',' outside of {@code <>}, {@code ()} and {@code []}. */
  private static List<String> splitTopLevel(String s) {
    List<String> parts = new ArrayList<String>();
    int depth = 0;
    int start = 0;
    for (int i = 0; i < s.length(); i++) {
      char c = s.charAt(i);
      if (c == '<' || c == '(' || c == '[') {
        depth++;
      } else if (c == '>' || c == ')' || c == ']') {
        depth--;
      } else if (c == ',' && depth == 0) {
        parts.add(s.substring(start, i));
        start = i + 1;
      }
    }
    parts.add(s.substring(start));
    return parts;
  }

  /** The whitespace separating a declared type from the parameter name, or -1. */
  private static int lastTopLevelSpace(String s) {
    int depth = 0;
    for (int i = s.length() - 1; i >= 0; i--) {
      char c = s.charAt(i);
      if (c == '>' || c == ')' || c == ']') {
        depth++;
      } else if (c == '<' || c == '(' || c == '[') {
        depth--;
      } else if (depth == 0 && (c == ' ' || c == '\t' || c == '\n' || c == '\r')) {
        return i;
      }
    }
    return -1;
  }

  private static List<String> names(List<Method> methods) {
    List<String> out = new ArrayList<String>();
    for (Method method : methods) {
      if (!out.contains(method.getName())) {
        out.add(method.getName());
      }
    }
    Collections.sort(out);
    return out;
  }

  private static List<String> descriptors(List<Method> methods) {
    List<String> out = new ArrayList<String>();
    for (Method method : methods) {
      out.add(method.getName() + method.getDescriptor());
    }
    Collections.sort(out);
    return out;
  }
}
