package com.flanksource.telltale.diag.jvm;

import com.flanksource.telltale.diag.jmx.JmxService;
import java.lang.management.*;
import java.util.*;
import javax.management.*;

public final class SettingsService {
  private SettingsService() {}

  public static Map<String, Object> snapshot() throws Exception {
    Map<String, Object> out = new LinkedHashMap<>();
    out.put("properties", new TreeMap<>(ManagementFactory.getRuntimeMXBean().getSystemProperties()));
    out.put("environment", new TreeMap<>(System.getenv()));
    out.put("runtime", new JmxService().readAll("java.lang:type=Runtime"));
    out.put("operatingSystem", new JmxService().readAll("java.lang:type=OperatingSystem"));
    return out;
  }

  public static String command(String command, String... args) throws Exception {
    MBeanServer server = ManagementFactory.getPlatformMBeanServer();
    ObjectName name = new ObjectName("com.sun.management:type=DiagnosticCommand");
    if (!server.isRegistered(name)) throw new UnsupportedOperationException("HotSpot DiagnosticCommand MBean is unavailable");
    return (String) server.invoke(name, command, new Object[] {args}, new String[] {String[].class.getName()});
  }
}
