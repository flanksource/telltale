import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Map;
import java.util.concurrent.atomic.AtomicBoolean;
import org.apache.catalina.Context;
import org.apache.catalina.Wrapper;
import org.apache.catalina.startup.Tomcat;
import org.springframework.context.annotation.Configuration;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.context.support.AnnotationConfigWebApplicationContext;
import org.springframework.web.server.ResponseStatusException;
import org.springframework.web.servlet.DispatcherServlet;
import org.springframework.web.servlet.config.annotation.EnableWebMvc;

final class DemoServer implements AutoCloseable {
  private final Tomcat tomcat = new Tomcat();
  private final AnnotationConfigWebApplicationContext spring = new AnnotationConfigWebApplicationContext();
  private final AtomicBoolean closed = new AtomicBoolean();

  DemoServer(int port, Path directory, DemoOperations operations) throws Exception {
    try {
      Path root = Files.createDirectories(directory.resolve("tomcat")).toAbsolutePath();
      tomcat.setBaseDir(root.toString());
      tomcat.setPort(port);
      tomcat.getConnector().setProperty("address", "127.0.0.1");
      Context context = tomcat.addContext("", root.toString());
      context.setParentClassLoader(Workload.class.getClassLoader());
      spring.setServletContext(context.getServletContext());
      spring.register(MvcConfiguration.class);
      spring.addBeanFactoryPostProcessor(factory -> factory.registerSingleton("demoController", new Controller(operations)));
      spring.refresh();
      Wrapper servlet = Tomcat.addServlet(context, "spring", new DispatcherServlet(spring));
      servlet.setAsyncSupported(true);
      servlet.setLoadOnStartup(1);
      context.addServletMappingDecoded("/", "spring");
      tomcat.start();
    } catch (Exception | Error error) {
      try { close(); } catch (Exception cleanup) { error.addSuppressed(cleanup); }
      throw error;
    }
  }

  String baseUrl() {
    return "http://127.0.0.1:" + tomcat.getConnector().getLocalPort();
  }

  @Override public void close() throws Exception {
    if (!closed.compareAndSet(false, true)) return;
    DemoResources.close(tomcat::stop, tomcat::destroy, spring::close);
  }

  @Configuration
  @EnableWebMvc
  public static class MvcConfiguration {}

  @RestController
  public static final class Controller {
    private final DemoOperations operations;

    Controller(DemoOperations operations) {
      this.operations = operations;
    }

    @GetMapping("/health") public Map<String, Object> health() {
      if (!operations.healthy()) throw new ResponseStatusException(HttpStatus.SERVICE_UNAVAILABLE, "Demo is not ready: " + operations.state());
      return operations.state();
    }

    @GetMapping("/demo/{id}") public String demo(@PathVariable("id") String id) throws Exception {
      operations.holdHttp(id);
      return "demo-" + id;
    }

    @GetMapping("/control/state") public Map<String, Object> state() {
      return operations.state();
    }

    @PostMapping("/control/hold") public Map<String, Object> hold() throws Exception {
      return operations.begin();
    }

    @PostMapping("/control/release") public Map<String, Object> release() throws Exception {
      return operations.release();
    }

    @PostMapping("/control/traffic") public Map<String, Object> traffic(@RequestParam("enabled") boolean enabled) throws Exception {
      return operations.traffic(enabled);
    }
  }
}
