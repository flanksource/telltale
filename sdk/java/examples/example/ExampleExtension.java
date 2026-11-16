package example;

import com.flanksource.telltale.DiagnosticExtension;
import com.flanksource.telltale.ExtensionContext;
import com.flanksource.telltale.OperationSpec;
import java.util.*;

public final class ExampleExtension implements DiagnosticExtension {
  public String id() {
    return "example";
  }

  public String version() {
    return "1.0.0";
  }

  public void start(ExtensionContext context) {
    Map<String, Object> schema = new LinkedHashMap<>();
    schema.put("type", "object");
    schema.put(
        "properties", Collections.singletonMap("name", Collections.singletonMap("type", "string")));
    schema.put("required", Collections.singletonList("name"));
    context.register(
        OperationSpec.named("extension.example.greet")
            .title("Greet an application operator")
            .description(
                "Example application-specific operation, discovered without client changes")
            .parameters(schema)
            .handle(
                params -> {
                  Object name = params.get("name");
                  if (!(name instanceof String))
                    throw new IllegalArgumentException("name is required");
                  return Collections.singletonMap("message", "Hello " + name);
                }));
    context.bindings(
        application ->
            Collections.singletonMap(
                "loaderType",
                application == null ? "bootstrap" : application.getClass().getName()));
  }

  public void close() {}
}
