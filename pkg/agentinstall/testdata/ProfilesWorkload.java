import com.sun.net.httpserver.HttpServer;
import java.io.*;
import java.net.*;
import java.sql.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicReference;

public final class ProfilesWorkload {
  private static final CountDownLatch httpEntered = new CountDownLatch(1);
  private static final CountDownLatch sqlEntered = new CountDownLatch(1);
  private static final CountDownLatch release = new CountDownLatch(1);
  private static final AtomicReference<Throwable> failure = new AtomicReference<>();

  public static int hold() throws InterruptedException {
    sqlEntered.countDown();
    if (!release.await(30, TimeUnit.SECONDS)) throw new IllegalStateException("SQL release timed out");
    return 7;
  }

  public static void main(String[] args) throws Exception {
    Class.forName("org.h2.Driver");
    HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    ExecutorService executor = Executors.newCachedThreadPool();
    server.setExecutor(executor);
    server.createContext("/", exchange -> {
      try {
        if (exchange.getRequestURI().getPath().equals("/hold")) {
          httpEntered.countDown();
          if (!release.await(30, TimeUnit.SECONDS)) throw new IllegalStateException("HTTP release timed out");
        }
        byte[] body = "complete".getBytes("UTF-8");
        exchange.sendResponseHeaders(200, body.length);
        exchange.getResponseBody().write(body);
      } catch (Exception error) { failure.set(error); }
      finally { exchange.close(); }
    });
    server.start();
    String address = "http://127.0.0.1:" + server.getAddress().getPort();
    try (Connection connection = DriverManager.getConnection("jdbc:h2:mem:profiles");
        Statement statement = connection.createStatement();
        BufferedReader input = new BufferedReader(new InputStreamReader(System.in, "UTF-8"))) {
      statement.execute("CREATE ALIAS HOLD FOR 'ProfilesWorkload.hold'");
      try (ResultSet warm = statement.executeQuery("SELECT 1")) {
        if (!warm.next() || warm.getInt(1) != 1) throw new IllegalStateException("JDBC warmup failed");
      }
      request(address + "/warm");
      System.out.println("FIXTURE READY");
      if (!"START".equals(input.readLine())) throw new IllegalStateException("expected START");
      Future<?> http = executor.submit(() -> {
        try { request(address + "/hold"); }
        catch (Exception error) { failure.set(error); }
      });
      Future<?> sql = executor.submit(() -> {
        try (ResultSet result = statement.executeQuery("SELECT HOLD()")) {
          if (!result.next() || result.getInt(1) != 7) throw new IllegalStateException("JDBC result changed");
        } catch (Exception error) { failure.set(error); }
      });
      if (!httpEntered.await(10, TimeUnit.SECONDS) || !sqlEntered.await(10, TimeUnit.SECONDS))
        throw new IllegalStateException("operations did not become active: " + failure.get());
      System.out.println("FIXTURE ACTIVE");
      if (!"FINISH".equals(input.readLine())) throw new IllegalStateException("expected FINISH");
      release.countDown();
      http.get(10, TimeUnit.SECONDS);
      sql.get(10, TimeUnit.SECONDS);
      if (failure.get() != null) throw new IllegalStateException("workload failed", failure.get());
      System.out.println("FIXTURE FINISHED");
      input.readLine();
    } finally {
      release.countDown();
      server.stop(0);
      executor.shutdownNow();
    }
  }

  private static void request(String address) throws Exception {
    HttpURLConnection connection = (HttpURLConnection) new URL(address).openConnection();
    connection.setConnectTimeout(5000);
    connection.setReadTimeout(30000);
    try (InputStream input = connection.getInputStream(); ByteArrayOutputStream body = new ByteArrayOutputStream()) {
      byte[] buffer = new byte[64];
      for (int count; (count = input.read(buffer)) != -1;) body.write(buffer, 0, count);
      if (!body.toString("UTF-8").equals("complete")) throw new IllegalStateException("HTTP body changed");
    } finally { connection.disconnect(); }
  }
}
