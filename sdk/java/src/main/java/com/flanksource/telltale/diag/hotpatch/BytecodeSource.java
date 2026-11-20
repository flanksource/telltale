package com.flanksource.telltale.diag.hotpatch;

import java.lang.instrument.ClassFileTransformer;
import java.lang.instrument.Instrumentation;
import java.lang.instrument.UnmodifiableClassException;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.security.ProtectionDomain;
import java.util.concurrent.atomic.AtomicReference;

/**
 * Reads the bytecode a class is <em>currently running</em>, not the bytes on disk. A no-op {@link
 * ClassFileTransformer} registered with {@code canRetransform=true} is handed the live class file
 * buffer during a {@link Instrumentation#retransformClasses} round-trip; it captures the buffer and
 * returns {@code null} so nothing is modified.
 *
 * <p>Anything that already rewrote the class (an APM agent, or a later hot patch) is therefore
 * reflected in what the browser shows — which is the whole point of a live class browser.
 */
public final class BytecodeSource {

  public static final String DIGEST_PREFIX = "sha256:";

  private final Instrumentation instrumentation;

  public BytecodeSource(Instrumentation instrumentation) {
    this.instrumentation = instrumentation;
  }

  public byte[] bytecodeOf(Class<?> target) {
    com.flanksource.telltale.observer.ObserverRuntime.assertTargetActive(target);
    if (!instrumentation.isRetransformClassesSupported()) {
      throw new IllegalStateException(
          "this JVM does not support class retransformation,"
              + " so the live bytecode of "
              + target.getName()
              + " cannot be read");
    }
    if (!instrumentation.isModifiableClass(target)) {
      throw new IllegalStateException(
          target.getName()
              + " is not a modifiable class"
              + " (primitive, array, or JVM-synthesized), so it has no readable class file");
    }
    final AtomicReference<byte[]> captured = new AtomicReference<>();
    final String internalName = target.getName().replace('.', '/');
    ClassFileTransformer capture =
        new ClassFileTransformer() {
          @Override
          public byte[] transform(
              ClassLoader loader,
              String className,
              Class<?> classBeingRedefined,
              ProtectionDomain protectionDomain,
              byte[] classfileBuffer) {
            if (classBeingRedefined == target && internalName.equals(className)) {
              captured.set(classfileBuffer.clone());
            }
            return null;
          }
        };
    instrumentation.addTransformer(capture, true);
    try {
      instrumentation.retransformClasses(target);
    } catch (UnmodifiableClassException e) {
      throw new IllegalStateException("cannot retransform " + target.getName() + ": " + e, e);
    } finally {
      instrumentation.removeTransformer(capture);
    }
    byte[] bytes = captured.get();
    if (bytes == null) {
      throw new IllegalStateException(
          "retransforming "
              + target.getName()
              + " produced no class file buffer — another transformer swallowed the retransform");
    }
    return bytes;
  }

  /** {@code sha256:<hex>} over the class bytes; the cache key and patch-drift detector. */
  public static String digest(byte[] bytes) {
    MessageDigest sha256;
    try {
      sha256 = MessageDigest.getInstance("SHA-256");
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException("SHA-256 is unavailable in this JVM", e);
    }
    byte[] hash = sha256.digest(bytes);
    StringBuilder out = new StringBuilder(DIGEST_PREFIX.length() + hash.length * 2);
    out.append(DIGEST_PREFIX);
    for (byte b : hash) {
      out.append(Character.forDigit((b >> 4) & 0xF, 16)).append(Character.forDigit(b & 0xF, 16));
    }
    return out.toString();
  }
}
