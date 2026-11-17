package observerfixture;

import java.lang.management.ManagementFactory;
import java.net.*;
import javax.management.ObjectName;

public final class NoRestartCaptureLoader extends URLClassLoader {
  private final ClassLoader runtime;

  public NoRestartCaptureLoader() throws Exception {
    super(new URL[] {NoRestartCaptureLoader.class.getProtectionDomain().getCodeSource().getLocation()}, ClassLoader.getSystemClassLoader());
    runtime = ManagementFactory.getPlatformMBeanServer().getClassLoaderFor(new ObjectName("com.flanksource.telltale:type=Diagnostics"));
  }

  protected synchronized Class<?> loadClass(String name, boolean resolve) throws ClassNotFoundException {
    if (name.startsWith("com.flanksource.telltale.")) return runtime.loadClass(name);
    if (name.equals("observerfixture.NoRestartOtelCapture") || name.equals("observerfixture.NoRestartSqlCapture")) {
      Class<?> loaded = findLoadedClass(name);
      if (loaded == null) loaded = findClass(name);
      if (resolve) resolveClass(loaded);
      return loaded;
    }
    return super.loadClass(name, resolve);
  }
}
