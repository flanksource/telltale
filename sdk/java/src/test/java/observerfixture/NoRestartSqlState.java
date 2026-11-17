package observerfixture;

import java.sql.DriverManager;
import java.sql.PreparedStatement;
import reactor.core.publisher.Flux;
import reactor.core.publisher.Mono;

/** Application SQL objects are created and exercised before Telltale is attached. */
public final class NoRestartSqlState {
  static java.sql.Connection jdbcConnection;
  public static PreparedStatement jdbc;
  static io.r2dbc.spi.Connection reactiveConnection;
  public static io.r2dbc.spi.Statement reactive;
  private NoRestartSqlState() {}

  public static void open() throws Exception {
    jdbcConnection = DriverManager.getConnection("jdbc:h2:mem:no-restart-sql");
    jdbc = jdbcConnection.prepareStatement("SELECT 1");
    if (!jdbc.execute()) throw new IllegalStateException("JDBC warm-up changed");
    io.r2dbc.spi.ConnectionFactory factory = io.r2dbc.spi.ConnectionFactories.get("r2dbc:h2:mem:///no-restart-sql-reactive");
    reactiveConnection = Mono.from(factory.create()).block();
    reactive = reactiveConnection.createStatement("SELECT 1");
    if (!Integer.valueOf(1).equals(Flux.from(reactive.execute())
        .flatMap(result -> result.map((row, metadata) -> row.get(0, Integer.class))).blockLast()))
      throw new IllegalStateException("R2DBC warm-up changed");
  }

  public static void close() throws Exception {
    try { if (reactiveConnection != null) Mono.from(reactiveConnection.close()).block(); }
    finally { try { if (jdbc != null) jdbc.close(); } finally { if (jdbcConnection != null) jdbcConnection.close(); } }
  }
}
