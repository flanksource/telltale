package com.flanksource.telltale.harness;

import java.io.InputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import org.apache.catalina.startup.Tomcat;

/**
 * Embedded Tomcat that deploys one WAR on 127.0.0.1, prints {@code READY <port>} and runs until
 * stdin reaches EOF. Usage: {@code TomcatHarness <absolute war path> <contextPath>}.
 */
public final class TomcatHarness {
  private TomcatHarness() {}

  public static void main(String[] args) throws Exception {
    if (args.length != 2)
      throw new IllegalArgumentException("usage: TomcatHarness <absolute war path> <contextPath>");
    Path war = Paths.get(args[0]);
    if (!war.isAbsolute() || !Files.isRegularFile(war))
      throw new IllegalArgumentException("war must be an absolute path to a file: " + args[0]);
    Path base = Files.createTempDirectory("telltale-harness-");
    Files.createDirectories(base.resolve("webapps"));
    Tomcat tomcat = new Tomcat();
    tomcat.setAddDefaultWebXmlToWebapp(false);
    tomcat.setBaseDir(base.toString());
    tomcat.setPort(0);
    tomcat.getConnector().setProperty("address", "127.0.0.1");
    tomcat.addWebapp(args[1], war.toString());
    tomcat.start();
    startWorkload();
    System.out.println("READY " + tomcat.getConnector().getLocalPort());
    System.out.flush();
    awaitEof(System.in);
    tomcat.stop();
    tomcat.destroy();
  }

  private static void startWorkload() {
    Thread workload =
        new Thread(
            () -> {
              long value = 0;
              while (true) {
                HarnessWorkload.compute(value++);
                try {
                  Thread.sleep(10);
                } catch (InterruptedException stop) {
                  return;
                }
              }
            },
            "telltale-harness-workload");
    workload.setDaemon(true);
    workload.start();
  }

  private static void awaitEof(InputStream input) throws Exception {
    byte[] buffer = new byte[256];
    while (input.read(buffer) != -1) {}
  }
}
