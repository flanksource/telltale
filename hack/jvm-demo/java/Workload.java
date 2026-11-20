import java.nio.file.Paths;
import java.nio.file.Path;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

public final class Workload {
  private static volatile DemoOperations operations;

  private Workload() {}

  public static double compute(long value) {
    return Math.sqrt(value + 1);
  }

  public static int hold(String api) throws InterruptedException {
    if (operations == null) throw new IllegalStateException("Demo operations are not initialized");
    return operations.holdSql(api);
  }

  public static void main(String[] args) throws Exception {
    if (args.length != 2) throw new IllegalArgumentException("Usage: Workload <port> <artifact-directory>");
    int port = Integer.parseInt(args[0]);
    if (port < 0 || port > 65535) throw new IllegalArgumentException("Invalid demo HTTP port: " + port);
    Path artifacts = Paths.get(args[1]);
    try (DemoOperations runtime = new DemoOperations(artifacts)) {
      operations = runtime;
      try (DemoServer server = new DemoServer(port, artifacts, runtime)) {
        run(runtime, server);
      }
    }
  }

  private static void run(DemoOperations runtime, DemoServer server) throws Exception {
    ScheduledExecutorService compute = Executors.newSingleThreadScheduledExecutor();
    Runtime.getRuntime().addShutdownHook(new Thread(() -> {
      compute.shutdownNow();
      try { DemoResources.close(runtime, server); }
      catch (Exception failure) { throw new IllegalStateException("Could not stop JVM demo", failure); }
    }, "demo-shutdown"));
    try {
      runtime.start(server.baseUrl());
      compute.scheduleWithFixedDelay(() -> {
        for (int value = 0; value < 32; value++) compute(value);
      }, 0, 10, TimeUnit.MILLISECONDS);
      System.out.println("FIXTURE READY " + server.baseUrl());
      new CountDownLatch(1).await();
    } finally {
      compute.shutdownNow();
    }
  }
}
