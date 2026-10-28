package com.flanksource.telltale;


public final class Attach {
  private Attach() {}

  public static void main(String[] args) throws Exception {
    if (args.length != 3)
      throw new IllegalArgumentException("Expected PID, agent JAR and config file");
    Class<?> vm = Class.forName("com.sun.tools.attach.VirtualMachine");
    Object attached = vm.getMethod("attach", String.class).invoke(null, args[0]);
    try {
      vm.getMethod("loadAgent", String.class, String.class).invoke(attached, args[1], args[2]);
    } finally {
      vm.getMethod("detach").invoke(attached);
    }
  }
}
