package com.flanksource.telltale;

import java.lang.management.ManagementFactory;
import javax.management.*;

public final class RuntimeOwner {
  private RuntimeOwner() {}

  public static ClassLoader loader() {
    MBeanServer server = ManagementFactory.getPlatformMBeanServer();
    try {
      ObjectName runtime = new ObjectName(RuntimeService.NAME);
      return server.isRegistered(runtime) ? server.getClassLoaderFor(runtime) : ClassLoader.getSystemClassLoader();
    } catch (JMException error) { throw new IllegalStateException("resolve JVM runtime owner", error); }
  }
}
