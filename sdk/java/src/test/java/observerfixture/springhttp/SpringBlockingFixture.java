package observerfixture.springhttp;

import static org.junit.Assert.*;

import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverSubscription;
import java.lang.reflect.Method;
import java.util.*;
import java.util.concurrent.Callable;
import net.bytebuddy.agent.ByteBuddyAgent;
import observerfixture.SpringVersionFixture;
import org.objectweb.asm.*;
import org.springframework.web.client.RestTemplate;

public final class SpringBlockingFixture {
  public static void restTemplate() throws Exception {
    try (SpringReactiveFixture.Fixture fixture = new SpringReactiveFixture.Fixture()) {
      RestTemplate existing = new RestTemplate();
      fixture.context.getBeanFactory().registerSingleton("existingRestTemplate", existing);
      assertSame(existing, fixture.context.getBean("existingRestTemplate"));
      assertEquals("warm", existing.getForObject(fixture.url("/warm"), String.class));
      observe("rest-template", () -> existing.getForObject(fixture.url("/warm"), String.class));
    }
  }

  public static void restClient() throws Exception {
    try (SpringReactiveFixture.Fixture fixture = new SpringReactiveFixture.Fixture()) {
      Object existing = client(fixture);
      assertEquals("warm", request(existing));
      observe("rest-client", () -> request(existing));
    }
  }

  public static void httpInterface() throws Exception {
    try (SpringReactiveFixture.Fixture fixture = new SpringReactiveFixture.Fixture()) {
      Object existing = client(fixture);
      ClassLoader loader = SpringBlockingFixture.class.getClassLoader();
      Class<?> clientType = loader.loadClass("org.springframework.web.client.RestClient");
      Class<?> adapterType = loader.loadClass("org.springframework.web.client.support.RestClientAdapter");
      Object adapter = adapterType.getMethod("create", clientType).invoke(null, existing);
      Class<?> exchangeType = loader.loadClass("org.springframework.web.service.invoker.HttpExchangeAdapter");
      Class<?> factoryType = loader.loadClass("org.springframework.web.service.invoker.HttpServiceProxyFactory");
      Object builder = factoryType.getMethod("builderFor", exchangeType).invoke(null, adapter);
      Object factory = builder.getClass().getMethod("build").invoke(builder);
      Class<?> contract = serviceContract(loader);
      Object service = factoryType.getMethod("createClient", Class.class).invoke(factory, contract);
      fixture.context.getBeanFactory().registerSingleton("existingHttpService", service);
      Method warm = contract.getMethod("warm");
      assertEquals("warm", warm.invoke(service));
      observe("http-interface", () -> (String) warm.invoke(service));
    }
  }

  private static Object client(SpringReactiveFixture.Fixture fixture) throws Exception {
    Class<?> type = SpringBlockingFixture.class.getClassLoader().loadClass("org.springframework.web.client.RestClient");
    Object existing = type.getMethod("create", String.class).invoke(null, fixture.url(""));
    fixture.context.getBeanFactory().registerSingleton("existingRestClient", existing);
    assertSame(existing, fixture.context.getBean("existingRestClient"));
    return existing;
  }

  private static String request(Object client) throws Exception {
    ClassLoader loader = SpringBlockingFixture.class.getClassLoader();
    Class<?> type = loader.loadClass("org.springframework.web.client.RestClient");
    Object get = type.getMethod("get").invoke(client);
    Class<?> uriType = loader.loadClass("org.springframework.web.client.RestClient$RequestHeadersUriSpec");
    Object request = uriType.getMethod("uri", String.class, Object[].class).invoke(get, "/warm", new Object[0]);
    Class<?> requestType = loader.loadClass("org.springframework.web.client.RestClient$RequestHeadersSpec");
    Object response = requestType.getMethod("retrieve").invoke(request);
    Class<?> responseType = loader.loadClass("org.springframework.web.client.RestClient$ResponseSpec");
    return (String) responseType.getMethod("body", Class.class).invoke(response, String.class);
  }

  @SuppressWarnings("unchecked")
  private static void observe(String name, Callable<String> call) throws Exception {
    ObserverRuntime.install(ByteBuddyAgent.install());
    try (ObserverSubscription capture = ObserverRuntime.subscribe("http", name, SpringHttpChecks.options());
        ObserverSubscription top = ObserverRuntime.subscribe("http_top", name + "-top", SpringHttpChecks.options())) {
      assertEquals("the application response must be unchanged", "warm", call.call());
      List<Map<String, Object>> rows = SpringHttpChecks.completed(capture, 2);
      SpringHttpChecks.identities(rows, "requestId");
      Set<Object> directions = new HashSet<>();
      for (Map<String, Object> row : rows) {
        directions.add(row.get("direction"));
        assertFalse("successful application call must stay successful", row.containsKey("error"));
        Map<String, Object> response = (Map<String, Object>) row.get("response");
        assertEquals(200, response.get("status"));
        assertEquals("warm", ((Map<String, Object>) response.get("content")).get("text"));
      }
      assertEquals(new HashSet<>(Arrays.asList("incoming", "outgoing")), directions);
      SpringHttpChecks.await(() -> top.snapshot().isEmpty());
    }
  }

  private static Class<?> serviceContract(ClassLoader loader) {
      String name = "observerfixture.springhttp.ExistingHttpService";
      ClassWriter writer = new ClassWriter(0);
      writer.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC | Opcodes.ACC_ABSTRACT | Opcodes.ACC_INTERFACE,
          name.replace('.', '/'), null, "java/lang/Object", null);
      MethodVisitor method = writer.visitMethod(Opcodes.ACC_PUBLIC | Opcodes.ACC_ABSTRACT, "warm", "()Ljava/lang/String;", null, null);
      AnnotationVisitor annotation = method.visitAnnotation("Lorg/springframework/web/service/annotation/GetExchange;", true);
      annotation.visit("value", "/warm");
      annotation.visitEnd();
      method.visitEnd();
      writer.visitEnd();
      byte[] bytes = writer.toByteArray();
      return SpringVersionFixture.define(loader, name, bytes);
  }
}
