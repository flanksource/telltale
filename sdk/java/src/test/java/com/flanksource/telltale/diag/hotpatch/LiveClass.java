package com.flanksource.telltale.diag.hotpatch;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.UncheckedIOException;

/**
 * Loads rewritten class bytes and runs them.
 *
 * <p>The tests deliberately execute what the transform produced rather than inspecting it. Defining
 * a class puts it through the JVM's own verifier, which is the authority on whether the recomputed
 * stack map frames are correct — and a wrong frame is the failure mode a bytecode-shape assertion
 * would miss entirely while reporting success.
 */
final class LiveClass extends ClassLoader {

  private final String className;
  private final byte[] bytecode;

  private LiveClass(String className, byte[] bytecode) {
    super(LiveClass.class.getClassLoader());
    this.className = className;
    this.bytecode = bytecode;
  }

  /** Defines {@code bytecode} as {@code className}, delegating everything else. */
  static Class<?> define(String className, byte[] bytecode) throws ClassNotFoundException {
    return new LiveClass(className, bytecode).loadClass(className);
  }

  /** The bytes a class is compiled to, read off the test classpath. */
  static byte[] bytecodeOf(Class<?> type) {
    String resource = "/" + type.getName().replace('.', '/') + ".class";
    try (InputStream in = type.getResourceAsStream(resource)) {
      if (in == null) {
        throw new IllegalStateException("no class file on the classpath for " + type.getName());
      }
      ByteArrayOutputStream out = new ByteArrayOutputStream();
      byte[] buffer = new byte[8192];
      for (int read = in.read(buffer); read >= 0; read = in.read(buffer)) {
        out.write(buffer, 0, read);
      }
      return out.toByteArray();
    } catch (IOException e) {
      throw new UncheckedIOException(e);
    }
  }

  @Override
  protected Class<?> loadClass(String name, boolean resolve) throws ClassNotFoundException {
    if (!className.equals(name)) {
      return super.loadClass(name, resolve);
    }
    Class<?> defined = findLoadedClass(name);
    if (defined == null) {
      defined = defineClass(name, bytecode, 0, bytecode.length);
    }
    if (resolve) {
      resolveClass(defined);
    }
    return defined;
  }
}
