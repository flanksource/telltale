package com.flanksource.telltale.diag.hotpatch;

import org.objectweb.asm.ClassReader;
import org.objectweb.asm.ClassWriter;

/**
 * A {@link ClassWriter} that computes stack map frames against the class loader of the class being
 * rewritten.
 *
 * <p>Java 8 class files (major 52) require a {@code StackMapTable}, so frames must be recomputed
 * for any method whose instructions change, and computing them means asking "what is the common
 * supertype of these two types?" at every merge point. {@link ClassWriter}'s default answer loads
 * those types through <em>ASM's own</em> loader — this WAR's — which cannot see a single
 * application class. Overriding {@link #getClassLoader()} points that resolution at the loader that
 * defined the target, which is the only one guaranteed to resolve every type the target's own code
 * mentions.
 *
 * <p>Constructing with the {@link ClassReader} is not just an optimisation: it shares the constant
 * pool, which is what lets ASM copy untouched methods verbatim instead of re-deriving their frames.
 * Methods this WAR does not rewrite therefore keep the exact bytes they were running, including
 * whatever an APM or tracing agent had already woven into them.
 */
final class TargetLoaderClassWriter extends ClassWriter {

  private final ClassLoader loader;

  TargetLoaderClassWriter(ClassReader reader, ClassLoader loader) {
    super(reader, COMPUTE_FRAMES);
    this.loader = loader;
  }

  @Override
  protected ClassLoader getClassLoader() {
    // Null is the bootstrap loader, which is the correct answer for a
    // bootstrap-loaded target rather than a missing one.
    return loader;
  }
}
