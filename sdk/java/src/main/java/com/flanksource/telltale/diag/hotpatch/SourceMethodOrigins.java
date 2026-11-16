package com.flanksource.telltale.diag.hotpatch;

import java.io.*;
import java.util.*;
import org.objectweb.asm.ClassReader;
import org.objectweb.asm.tree.*;

final class SourceMethodOrigins {
  private SourceMethodOrigins() {}

  static Map<String, MethodNode> read(Class<?> type, MethodNode method) {
    try (InputStream input = ClassArtifact.open(type)) {
      if (input == null) throw unavailable(type, method, "the authoritative code-source class artifact is missing");
      ClassNode original = new ClassNode();
      new ClassReader(input).accept(original, ClassReader.SKIP_DEBUG | ClassReader.SKIP_FRAMES);
      if (!original.name.equals(type.getName().replace('.', '/')))
        throw unavailable(type, method, "the code-source artifact names a different class");
      Map<String, MethodNode> methods = new LinkedHashMap<>();
      for (MethodNode candidate : original.methods) methods.put(candidate.name + candidate.desc, candidate);
      return methods;
    } catch (IOException | SecurityException | IllegalArgumentException error) {
      throw new IllegalStateException(unavailable(type, method, "the code-source artifact cannot be read").getMessage(), error);
    }
  }

  static IllegalStateException unavailable(Class<?> type, MethodNode method, String reason) {
    return new IllegalStateException("safe source editing unavailable for " + type.getName() + "#" + method.name + method.desc + ": " + reason);
  }
}
