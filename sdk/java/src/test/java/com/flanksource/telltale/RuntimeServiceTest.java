package com.flanksource.telltale;

import static org.junit.Assert.*;

import java.io.File;
import java.lang.management.ManagementFactory;
import java.net.*;
import java.util.*;
import javax.management.*;
import org.junit.Test;

public class RuntimeServiceTest {
  @Test
  public void sharesTheRuntimeAcrossIndependentApplicationClassloaders() throws Exception {
    List<URL> paths = new ArrayList<>();
    for (String path : System.getProperty("java.class.path").split(File.pathSeparator))
      paths.add(new File(path).toURI().toURL());
    ObjectName name = new ObjectName(RuntimeService.NAME);
    try (URLClassLoader first =
            new URLClassLoader(
                paths.toArray(new URL[0]), ClassLoader.getSystemClassLoader().getParent());
        URLClassLoader second =
            new URLClassLoader(
                paths.toArray(new URL[0]), ClassLoader.getSystemClassLoader().getParent())) {
      Class<?> owner = first.loadClass("com.flanksource.telltale.RuntimeService");
      assertNotSame(RuntimeService.class, owner);
      owner.getMethod("acquire", Map.class).invoke(null, Collections.emptyMap());
      second
          .loadClass("com.flanksource.telltale.RuntimeService")
          .getMethod("acquire", Map.class)
          .invoke(null, Collections.emptyMap());
      Map<String, Object> request = new LinkedHashMap<>();
      request.put("operation", "jvm.jmx.search");
      request.put("params", Collections.singletonMap("pattern", "java.lang:type=Runtime"));
      Object result =
          ManagementFactory.getPlatformMBeanServer()
              .invoke(name, "call", new Object[] {request}, new String[] {Map.class.getName()});
      assertEquals(Collections.singletonList("java.lang:type=Runtime"), result);
    } finally {
      ManagementFactory.getPlatformMBeanServer().unregisterMBean(name);
    }
  }
}
