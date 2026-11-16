package com.flanksource.telltale.diag.jvm;

import static org.junit.jupiter.api.Assertions.*;
import java.nio.file.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class ResourceSettingsTest {
  @TempDir Path directory;

  @Test void resolvesV2MembershipRelativeToMountRoot() throws Exception {
    Path mount = Files.createDirectory(directory.resolve("mount"));
    Path member = Files.createDirectory(mount.resolve("worker"));
    Files.write(member.resolve("memory.max"), "536870912".getBytes(StandardCharsets.UTF_8));
    Files.write(member.resolve("cpu.max"), "100000 100000".getBytes(StandardCharsets.UTF_8));
    Files.write(directory.resolve("cgroup"), "0::/tenant/worker\n".getBytes(StandardCharsets.UTF_8));
    Files.write(directory.resolve("mountinfo"), ("1 2 0:1 /tenant " + mount + " rw - cgroup2 cgroup rw\n").getBytes(StandardCharsets.UTF_8));
    Map<?, ?> settings = (Map<?, ?>) ResourceSettings.cgroups(directory).get(member.toString());
    assertEquals("536870912", settings.get("memory.max"));
    assertEquals("100000 100000", settings.get("cpu.max"));
  }

  @Test void resolvesV1ControllerMount() throws Exception {
    Path mount = Files.createDirectory(directory.resolve("mount"));
    Path member = Files.createDirectory(mount.resolve("worker"));
    Files.write(member.resolve("memory.limit_in_bytes"), "536870912".getBytes(StandardCharsets.UTF_8));
    Files.write(directory.resolve("cgroup"), "1:memory:/worker\n".getBytes(StandardCharsets.UTF_8));
    Files.write(directory.resolve("mountinfo"), ("1 2 0:1 / " + mount + " rw - cgroup cgroup rw,memory\n").getBytes(StandardCharsets.UTF_8));
    Map<?, ?> settings = (Map<?, ?>) ResourceSettings.cgroups(directory).get(member.toString());
    assertEquals("536870912", settings.get("memory.limit_in_bytes"));
  }
}
