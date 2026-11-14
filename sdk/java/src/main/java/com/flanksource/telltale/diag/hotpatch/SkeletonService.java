package com.flanksource.telltale.diag.hotpatch;

import java.lang.reflect.Constructor;
import java.lang.reflect.Field;
import java.lang.reflect.GenericArrayType;
import java.lang.reflect.Member;
import java.lang.reflect.Method;
import java.lang.reflect.Modifier;
import java.lang.reflect.ParameterizedType;
import java.lang.reflect.Type;
import java.lang.reflect.TypeVariable;
import java.lang.reflect.WildcardType;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Comparator;
import java.util.List;

/**
 * Generates a compilable stand-in for a live class, so one replacement method can be compiled
 * against the class's own fields and methods without the class's source — and without a decompiler.
 *
 * <p>The author's method has to type-check against everything it touches: the fields it reads, the
 * helpers it calls, the superclass it inherits from. It does <em>not</em> need any of their
 * <em>bodies</em>. This runs inside the JVM holding the loaded {@link Class}, so reflection gives
 * that shape exactly — generics included — which a decompilation only approximates and, on a class
 * an agent has already instrumented, frequently fails to reproduce at all.
 *
 * <p>Only the target method's bytecode is kept ({@link MethodSplicer} splices it into the live
 * bytes and discards the rest), so the skeleton is free to be dishonest about everything else:
 * every other body throws, and {@code final} is dropped from fields because definite-assignment
 * rules would otherwise demand initialisers that serve no purpose here.
 */
public final class SkeletonService {

  /**
   * A generated stand-in, and where in it the author's own source begins.
   *
   * <p>The line matters as much as the source: javac reports diagnostics against the file it
   * compiled, and the author never sees that file. Without the offset, "cannot find symbol at line
   * 61" would point into generated boilerplate for a method the author wrote 12 lines of.
   */
  public static final class Skeleton {
    private final String source;
    private final int methodLine;

    Skeleton(String source, int methodLine) {
      this.source = source;
      this.methodLine = methodLine;
    }

    public String getSource() {
      return source;
    }

    /** 1-based line of {@code methodSource}'s first line within {@link #getSource()}. */
    public int getMethodLine() {
      return methodLine;
    }
  }

  /**
   * @param target the live class the replacement belongs to.
   * @param methodSource the author's method declaration, verbatim, braces and all.
   * @param replaced the method it replaces, so the generated stand-in is left out.
   */
  public Skeleton skeleton(
      Class<?> target, String methodSource, org.objectweb.asm.commons.Method replaced) {
    refuseUnsupported(target);
    StringBuilder out = new StringBuilder();
    // Derived from the binary name rather than getPackage(), which answers
    // only for a loader that has defined the package object.
    int dot = target.getName().lastIndexOf('.');
    if (dot > 0) {
      out.append("package ").append(target.getName().substring(0, dot)).append(";\n\n");
    }
    // The stand-in bodies are unreachable and the dropped `final`s are
    // deliberate; javac's warnings about either would be noise in a report
    // the author reads to find a real compile error.
    out.append("@SuppressWarnings(\"all\")\n");
    appendDeclaration(out, target);
    out.append(" {\n");

    for (Field field : sorted(target.getDeclaredFields())) {
      if (field.isSynthetic()) {
        continue;
      }
      appendField(out, field);
    }
    for (Constructor<?> constructor : sorted(target.getDeclaredConstructors())) {
      if (constructor.isSynthetic() || matches(constructor, replaced)) {
        continue;
      }
      appendConstructor(out, target, constructor);
    }
    for (Method method : sorted(target.getDeclaredMethods())) {
      if (method.isSynthetic() || method.isBridge() || matches(method, replaced)) {
        continue;
      }
      appendMethod(out, method);
    }

    out.append('\n');
    int methodLine = 1;
    for (int i = 0; i < out.length(); i++) {
      if (out.charAt(i) == '\n') {
        methodLine++;
      }
    }
    out.append(methodSource.trim()).append("\n}\n");
    return new Skeleton(out.toString(), methodLine);
  }

  /**
   * The skeleton is a plain class body; an enum's constants, an interface's lack of one and an
   * annotation's element syntax are each a different generator. Refusing names the limit instead of
   * emitting source that fails to compile for a reason the author cannot act on.
   */
  private static void refuseUnsupported(Class<?> target) {
    String kind = null;
    if (target.isEnum()) {
      kind = "an enum";
    } else if (target.isAnnotation()) {
      kind = "an annotation";
    } else if (target.isInterface()) {
      kind = "an interface";
    } else if (target.isArray() || target.isPrimitive()) {
      kind = "not a source-level class";
    }
    if (kind != null) {
      throw new IllegalArgumentException(
          target.getName()
              + " is "
              + kind
              + ", which cannot be given a compilable stand-in."
              + " Instrument the method instead (jvm.trace), which needs no compiler");
    }
  }

  /** {@code public abstract class Name<T> extends Super implements I} */
  private static void appendDeclaration(StringBuilder out, Class<?> target) {
    int access = target.getModifiers() & (Modifier.PUBLIC | Modifier.ABSTRACT | Modifier.FINAL);
    if (access != 0) {
      out.append(Modifier.toString(access)).append(' ');
    }
    // A nested class is generated as a top-level class literally named
    // Outer$Inner: '$' is a legal identifier character, the binary name is
    // what a redefinition matches on, and the alternative — reproducing the
    // whole enclosing class — would defeat the point of a skeleton.
    out.append("class ").append(simpleBinaryName(target));
    appendTypeParameters(out, target.getTypeParameters());
    if (target.getSuperclass() != null && target.getSuperclass() != Object.class) {
      out.append(" extends ").append(typeName(target.getGenericSuperclass()));
    }
    Type[] interfaces = target.getGenericInterfaces();
    for (int i = 0; i < interfaces.length; i++) {
      out.append(i == 0 ? " implements " : ", ").append(typeName(interfaces[i]));
    }
  }

  private static void appendField(StringBuilder out, Field field) {
    // `final` is dropped on purpose: a final field would demand an
    // initialiser or a constructor assignment, and no field of this skeleton
    // is ever read at runtime.
    int access = field.getModifiers() & Modifier.fieldModifiers() & ~Modifier.FINAL;
    out.append("    ")
        .append(modifiers(access))
        .append(typeName(field.getGenericType()))
        .append(' ')
        .append(field.getName())
        .append(";\n");
  }

  private static void appendMethod(StringBuilder out, Method method) {
    int access = method.getModifiers() & Modifier.methodModifiers();
    out.append("    ").append(modifiers(access));
    if (method.getTypeParameters().length > 0) {
      appendTypeParameters(out, method.getTypeParameters());
      out.append(' ');
    }
    out.append(typeName(method.getGenericReturnType())).append(' ').append(method.getName());
    appendParameters(out, method.getGenericParameterTypes(), method.isVarArgs());
    appendThrows(out, method.getGenericExceptionTypes());
    if (Modifier.isAbstract(access) || Modifier.isNative(access)) {
      out.append(";\n");
      return;
    }
    out.append(" { throw new UnsupportedOperationException(); }\n");
  }

  /**
   * A constructor whose body is an explicit {@code super(...)}.
   *
   * <p>Explicit because the superclass may have no no-argument constructor — {@code ActivityDal
   * extends BaseDal} is the case this exists for. The arguments are there to select an overload,
   * never to run: nothing ever constructs this class.
   */
  private static void appendConstructor(
      StringBuilder out, Class<?> target, Constructor<?> constructor) {
    out.append("    ")
        .append(modifiers(constructor.getModifiers() & Modifier.constructorModifiers()))
        .append(simpleBinaryName(target));
    appendParameters(out, constructor.getGenericParameterTypes(), constructor.isVarArgs());
    appendThrows(out, constructor.getGenericExceptionTypes());
    out.append(" {");
    String superCall = superCall(target.getSuperclass());
    if (!superCall.isEmpty()) {
      out.append(' ').append(superCall);
    }
    out.append(" }\n");
  }

  /**
   * {@code super(...)} against the superclass's narrowest visible constructor, every argument cast
   * so the overload is unambiguous.
   */
  private static String superCall(Class<?> superclass) {
    if (superclass == null || superclass == Object.class) {
      return "";
    }
    Constructor<?> narrowest = null;
    for (Constructor<?> candidate : superclass.getDeclaredConstructors()) {
      if (candidate.isSynthetic() || Modifier.isPrivate(candidate.getModifiers())) {
        continue;
      }
      if (narrowest == null
          || candidate.getParameterTypes().length < narrowest.getParameterTypes().length) {
        narrowest = candidate;
      }
    }
    if (narrowest == null) {
      throw new IllegalStateException(
          superclass.getName()
              + " has no constructor this skeleton can call,"
              + " so its subclass cannot be given a compilable stand-in."
              + " Instrument the method instead (jvm.trace), which needs no compiler");
    }
    Class<?>[] parameters = narrowest.getParameterTypes();
    if (parameters.length == 0) {
      return "";
    }
    StringBuilder call = new StringBuilder("super(");
    for (int i = 0; i < parameters.length; i++) {
      call.append(i == 0 ? "" : ", ")
          .append('(')
          .append(typeName(parameters[i]))
          .append(") ")
          .append(zeroValue(parameters[i]));
    }
    return call.append(");").toString();
  }

  private static void appendParameters(StringBuilder out, Type[] parameters, boolean varargs) {
    out.append('(');
    for (int i = 0; i < parameters.length; i++) {
      String rendered = typeName(parameters[i]);
      if (varargs && i == parameters.length - 1 && rendered.endsWith("[]")) {
        rendered = rendered.substring(0, rendered.length() - 2) + "...";
      }
      out.append(i == 0 ? "" : ", ").append(rendered).append(" p").append(i);
    }
    out.append(')');
  }

  private static void appendThrows(StringBuilder out, Type[] thrown) {
    for (int i = 0; i < thrown.length; i++) {
      out.append(i == 0 ? " throws " : ", ").append(typeName(thrown[i]));
    }
  }

  private static void appendTypeParameters(StringBuilder out, TypeVariable<?>[] variables) {
    if (variables.length == 0) {
      return;
    }
    out.append('<');
    for (int i = 0; i < variables.length; i++) {
      out.append(i == 0 ? "" : ", ").append(variables[i].getName());
      int written = 0;
      for (Type bound : variables[i].getBounds()) {
        if (bound == Object.class) {
          continue;
        }
        out.append(written++ == 0 ? " extends " : " & ").append(typeName(bound));
      }
    }
    out.append('>');
  }

  /**
   * A type as it would be written in source.
   *
   * <p>{@code Type.getTypeName()} is not usable here: it renders a nested class with its binary
   * {@code $} separator, and {@code com.foo.Map$Entry} is not a type reference javac resolves.
   * Canonical names are, which is why every {@link Class} goes through {@link
   * Class#getCanonicalName()} and the generic forms are composed from the parts rather than taken
   * whole.
   */
  private static String typeName(Type type) {
    if (type instanceof Class) {
      Class<?> raw = (Class<?>) type;
      String canonical = raw.getCanonicalName();
      // Anonymous and local classes have no canonical name and cannot be
      // written in source at all; the binary name at least names what is
      // missing when javac then rejects it.
      return canonical != null ? canonical : raw.getName();
    }
    if (type instanceof ParameterizedType) {
      ParameterizedType parameterized = (ParameterizedType) type;
      StringBuilder out = new StringBuilder(typeName(parameterized.getRawType())).append('<');
      Type[] arguments = parameterized.getActualTypeArguments();
      for (int i = 0; i < arguments.length; i++) {
        out.append(i == 0 ? "" : ", ").append(typeName(arguments[i]));
      }
      return out.append('>').toString();
    }
    if (type instanceof GenericArrayType) {
      return typeName(((GenericArrayType) type).getGenericComponentType()) + "[]";
    }
    if (type instanceof WildcardType) {
      WildcardType wildcard = (WildcardType) type;
      for (Type bound : wildcard.getLowerBounds()) {
        return "? super " + typeName(bound);
      }
      for (Type bound : wildcard.getUpperBounds()) {
        if (bound != Object.class) {
          return "? extends " + typeName(bound);
        }
      }
      return "?";
    }
    // A TypeVariable renders as its own name, which the enclosing
    // declaration has already introduced.
    return type.getTypeName();
  }

  /**
   * The name a nested class is declared under here: {@code Outer$Inner}, which is both a legal Java
   * identifier and the binary name a redefinition matches.
   */
  private static String simpleBinaryName(Class<?> target) {
    String name = target.getName();
    int dot = name.lastIndexOf('.');
    return dot < 0 ? name : name.substring(dot + 1);
  }

  private static String zeroValue(Class<?> type) {
    if (!type.isPrimitive()) {
      return "null";
    }
    if (type == boolean.class) {
      return "false";
    }
    if (type == char.class) {
      return "'\\0'";
    }
    if (type == long.class) {
      return "0L";
    }
    if (type == float.class) {
      return "0F";
    }
    if (type == double.class) {
      return "0D";
    }
    return "0";
  }

  private static boolean matches(Method method, org.objectweb.asm.commons.Method replaced) {
    return method.getName().equals(replaced.getName())
        && org.objectweb.asm.Type.getMethodDescriptor(method).equals(replaced.getDescriptor());
  }

  private static boolean matches(
      Constructor<?> constructor, org.objectweb.asm.commons.Method replaced) {
    return "<init>".equals(replaced.getName())
        && org.objectweb.asm.Type.getConstructorDescriptor(constructor)
            .equals(replaced.getDescriptor());
  }

  private static String modifiers(int access) {
    String rendered = Modifier.toString(access);
    return rendered.isEmpty() ? "" : rendered + " ";
  }

  /** Deterministic output, so two skeletons of the same class compare equal. */
  private static <T extends Member> List<T> sorted(T[] members) {
    List<T> out = new ArrayList<T>(Arrays.asList(members));
    out.sort(Comparator.comparing(Object::toString));
    return out;
  }
}
