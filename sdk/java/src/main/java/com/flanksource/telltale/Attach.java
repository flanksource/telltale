package com.flanksource.telltale;

import java.lang.reflect.InvocationTargetException;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;

public final class Attach {
  private Attach() {}

  public static void main(String[] args) throws Exception {
    if (args.length != 4)
      throw new IllegalArgumentException("Expected PID, agent JAR, config file and upgrade flag");
    Class<?> vm = Class.forName("com.sun.tools.attach.VirtualMachine");
    Object attached = vm.getMethod("attach", String.class).invoke(null, args[0]);
    Path result = Files.createTempFile(Paths.get(args[2]).toAbsolutePath().getParent(), "attach-", ".result");
    try {
      try {
        vm.getMethod("loadAgent", String.class, String.class).invoke(attached, args[1], args[2] + "\n" + args[1] + "\n" + args[3] + "\n" + result);
      } catch (InvocationTargetException failure) {
        String status = new String(Files.readAllBytes(result), StandardCharsets.UTF_8);
        if (status.startsWith("error\n")) throw new IllegalStateException(status.substring(6), failure.getCause());
        throw failure;
      }
      String status = new String(Files.readAllBytes(result), StandardCharsets.UTF_8);
      if (!status.equals("ok")) throw new IllegalStateException("agent did not acknowledge attachment: " + status);
    } finally {
      try { vm.getMethod("detach").invoke(attached); }
      finally { Files.deleteIfExists(result); }
    }
  }
}
