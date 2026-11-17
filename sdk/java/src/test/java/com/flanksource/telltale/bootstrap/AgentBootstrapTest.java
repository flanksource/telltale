package com.flanksource.telltale.bootstrap;

import static org.junit.Assert.*;

import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import java.util.jar.*;
import javax.tools.ToolProvider;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.Test;

public class AgentBootstrapTest {
  @Test public void replacesStructurallyDifferentRuntimesAndRefusesBusyOrInvalidUpgrades() throws Exception {
    Path directory = Files.createTempDirectory(Files.createDirectories(Paths.get(".tmp")), "upgrade-");
    Path config = Files.write(directory.resolve("config.json"), "{}".getBytes(StandardCharsets.UTF_8));
    Path first = runtime(directory.resolve("one"), "one", "int first;");
    Path second = runtime(directory.resolve("two"), "two", "long second; public String added() { return \"new\"; }");
    java.lang.instrument.Instrumentation instrumentation = ByteBuddyAgent.install();
    AgentBootstrap.install(first, config, instrumentation, false);
    assertEquals("one", System.getProperty("telltale.upgrade.fixture"));
    try {
      AgentBootstrap.install(second, config, instrumentation, false);
      fail("replacement must require explicit upgrade");
    } catch (IllegalStateException expected) { assertTrue(expected.getMessage().contains("upgrade")); }
    System.setProperty("telltale.upgrade.busy", "true");
    try {
      AgentBootstrap.install(second, config, instrumentation, true);
      fail("busy runtime must be retained");
    } catch (IllegalStateException expected) { assertTrue(expected.getMessage().contains("busy")); }
    assertEquals("one", System.getProperty("telltale.upgrade.fixture"));
    System.clearProperty("telltale.upgrade.busy");
    Path invalid = Files.write(directory.resolve("invalid.jar"), new byte[] {1, 2, 3});
    try {
      AgentBootstrap.install(invalid, config, instrumentation, true);
      fail("invalid artifact must be rejected before stopping the runtime");
    } catch (IOException expected) { assertEquals("one", System.getProperty("telltale.upgrade.fixture")); }
    AgentBootstrap.install(second, config, instrumentation, true);
    assertEquals("two", System.getProperty("telltale.upgrade.fixture"));
    AgentBootstrap.install(second, config, instrumentation, false);
    assertEquals("two", System.getProperty("telltale.upgrade.fixture"));
    Path broken = runtime(directory.resolve("broken"), "broken", "boolean incompatible;");
    System.setProperty("telltale.upgrade.fail", "broken");
    try {
      AgentBootstrap.install(broken, config, instrumentation, true);
      fail("failed activation must report its error");
    } catch (IllegalStateException expected) { assertTrue(expected.getMessage().contains("previous runtime was restored")); }
    finally { System.clearProperty("telltale.upgrade.fail"); }
    assertEquals("two", System.getProperty("telltale.upgrade.fixture"));
  }

  private Path runtime(Path directory, String version, String fields) throws Exception {
    Path source = directory.resolve("com/flanksource/telltale/Agent.java");
    Files.createDirectories(source.getParent());
    String code = "package com.flanksource.telltale; public final class Agent { " + fields
        + " public static void validateRuntime(String config) {}"
        + " public static void agentmain(String config, java.lang.instrument.Instrumentation inst) { if (\"" + version + "\".equals(System.getProperty(\"telltale.upgrade.fail\"))) throw new IllegalStateException(\"activation failed\"); System.setProperty(\"telltale.upgrade.fixture\", \"" + version + "\"); }"
        + " public static void prepareUpgrade() { if (Boolean.getBoolean(\"telltale.upgrade.busy\")) throw new IllegalStateException(\"busy runtime\"); }"
        + " public static void stopRuntime() { System.clearProperty(\"telltale.upgrade.fixture\"); } }";
    Files.write(source, code.getBytes(StandardCharsets.UTF_8));
    assertEquals(0, ToolProvider.getSystemJavaCompiler().run(null, null, null, "-d", directory.toString(), source.toString()));
    Path jar = directory.resolve("runtime.jar");
    try (JarOutputStream output = new JarOutputStream(Files.newOutputStream(jar))) {
      output.putNextEntry(new JarEntry("com/flanksource/telltale/Agent.class"));
      Files.copy(directory.resolve("com/flanksource/telltale/Agent.class"), output);
      output.closeEntry();
    }
    return jar;
  }
}
