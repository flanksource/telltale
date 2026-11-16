package com.flanksource.telltale.diag.jvm;

import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;

public final class ResourceSettings {
  private ResourceSettings() {}

  public static Map<String, Object> snapshot() throws IOException {
    Map<String, Object> out = new LinkedHashMap<>();
    List<Map<String, Object>> disks = new ArrayList<>();
    for (File root : File.listRoots()) {
      Map<String, Object> disk = new LinkedHashMap<>();
      disk.put("path", root.getAbsolutePath());
      disk.put("total", root.getTotalSpace());
      disk.put("usable", root.getUsableSpace());
      disk.put("free", root.getFreeSpace());
      disks.add(disk);
    }
    out.put("filesystems", disks);
    if (!System.getProperty("os.name").equals("Linux")) {
      out.put("linux", Collections.singletonMap("status", "not-applicable"));
      return out;
    }
    for (String file : Arrays.asList("limits", "status", "cgroup", "mountinfo")) {
      out.put(file, read(Paths.get("/proc/self", file)));
    }
    out.put("cgroups", cgroups(Paths.get("/proc/self")));
    return out;
  }

  static Map<String, Object> cgroups(Path proc) throws IOException {
    Map<String, Object> out = new LinkedHashMap<>();
    for (String group : read(proc.resolve("cgroup")).split("\n")) {
      String[] membership = group.split(":", 3);
      if (membership.length != 3) throw new IOException("invalid cgroup membership: " + group);
      for (String mount : read(proc.resolve("mountinfo")).split("\n")) {
        String[] fields = mount.split(" ");
        int separator = Arrays.asList(fields).indexOf("-");
        if (separator < 0 || fields.length <= separator + 3) throw new IOException("invalid mountinfo");
        boolean v2 = fields[separator + 1].equals("cgroup2") && membership[1].isEmpty();
        boolean v1 = fields[separator + 1].equals("cgroup") && !membership[1].isEmpty()
            && Arrays.asList(fields[separator + 3].split(",")).containsAll(Arrays.asList(membership[1].split(",")));
        if (!v1 && !v2) continue;
        String root = fields[3].replace("\\040", " ");
        String path = membership[2];
        if (!root.equals("/") && path.startsWith(root + "/")) path = path.substring(root.length());
        else if (path.equals(root)) path = "/";
        Path directory = Paths.get(fields[4].replace("\\040", " ")).resolve(path.substring(1)).normalize();
        Map<String, Object> limits = new LinkedHashMap<>();
        for (String file : Arrays.asList("cpu.max", "cpu.stat", "cpuset.cpus.effective", "memory.max", "memory.current", "memory.events", "cpu.cfs_quota_us", "cpu.cfs_period_us", "memory.limit_in_bytes", "memory.usage_in_bytes")) {
          Path setting = directory.resolve(file);
          if (Files.exists(setting)) limits.put(file, read(setting));
        }
        out.put(directory.toString(), limits);
      }
    }
    return out;
  }

  private static String read(Path path) throws IOException {
    try (InputStream input = Files.newInputStream(path); ByteArrayOutputStream out = new ByteArrayOutputStream()) {
      byte[] buffer = new byte[8192];
      int count;
      while ((count = input.read(buffer)) != -1) {
        if (out.size() + count > 1024 * 1024) throw new IOException(path + " exceeds 1 MiB");
        out.write(buffer, 0, count);
      }
      return new String(out.toByteArray(), StandardCharsets.UTF_8);
    }
  }
}
