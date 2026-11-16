package com.flanksource.telltale.observer.sql;

import observerfixture.SpringVersionFixture;
import observerfixture.springsql.SpringJdbcClientFixture;
import org.junit.Test;

public class SpringJdbcClientObserverTest {
  @Test public void observesAnExistingSpring62JdbcClient() throws Exception {
    SpringVersionFixture.run("telltale.sql.spring6", "6.2.", SpringJdbcClientFixture.class.getName(), "existingClient");
  }

  @Test public void observesAnExistingSpring70JdbcClient() throws Exception {
    SpringVersionFixture.run("telltale.sql.spring7", "7.0.", SpringJdbcClientFixture.class.getName(), "existingClient");
  }
}
