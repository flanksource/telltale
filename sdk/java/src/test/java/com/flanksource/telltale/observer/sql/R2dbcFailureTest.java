package com.flanksource.telltale.observer.sql;

import static org.junit.jupiter.api.Assertions.*;

import io.r2dbc.spi.*;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.reactivestreams.Publisher;
import reactor.core.publisher.Flux;
import reactor.core.publisher.Mono;

class R2dbcFailureTest {
  @Test
  @SuppressWarnings("unchecked")
  void isolatesObserverErrorsAtEveryDeferredBoundaryIncludingFailureReporting() {
    Connection connection = Mono.from(ConnectionFactories.get("r2dbc:h2:mem:///sql-failure-isolation").create()).block();
    try {
      for (String boundary : Arrays.asList("start", "phase", "end", "reporter")) {
        Statement statement = connection.createStatement("SELECT 1");
        List<Throwable> failures = new ArrayList<>();
        Object observed = new R2dbcSupport().observe(statement, statement.execute(), Collections.singletonMap("sql", "SELECT ?"),
            row -> { if (boundary.equals("start") || boundary.equals("reporter")) throw new AssertionError("observer start"); },
            row -> { if (boundary.equals("phase")) throw new AssertionError("observer phase"); },
            (row, error) -> { if (boundary.equals("end")) throw new AssertionError("observer end"); },
            error -> { failures.add(error); if (boundary.equals("reporter")) throw new AssertionError("observer reporter"); });
        assertEquals(Integer.valueOf(1), Flux.from((Publisher<? extends Result>) observed)
            .flatMap(result -> result.map((row, metadata) -> row.get(0, Integer.class))).blockLast());
        assertFalse(failures.isEmpty(), boundary);
        assertInstanceOf(AssertionError.class, failures.get(0));
      }
    } finally {
      Mono.from(connection.close()).block();
    }
  }

  @Test
  @SuppressWarnings("unchecked")
  void preservesApplicationMapperFailures() {
    Connection connection = Mono.from(ConnectionFactories.get("r2dbc:h2:mem:///sql-mapper-failure").create()).block();
    try {
      Statement statement = connection.createStatement("SELECT 1");
      List<Throwable> failures = new ArrayList<>();
      Object observed = new R2dbcSupport().observe(statement, statement.execute(), Collections.singletonMap("sql", "SELECT ?"),
          row -> {}, row -> {}, (row, error) -> {}, failures::add);
      IllegalStateException expected = new IllegalStateException("application mapper");
      IllegalStateException actual = assertThrows(IllegalStateException.class, () -> Flux.from((Publisher<? extends Result>) observed)
          .flatMap(result -> result.map((row, metadata) -> { throw expected; })).blockLast());
      assertSame(expected, actual);
      assertTrue(failures.isEmpty());
    } finally {
      Mono.from(connection.close()).block();
    }
  }
}
