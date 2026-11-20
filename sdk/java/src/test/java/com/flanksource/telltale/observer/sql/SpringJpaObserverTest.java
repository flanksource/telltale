package com.flanksource.telltale.observer.sql;

import static org.junit.Assert.*;

import com.flanksource.telltale.observer.ObserverRuntime;
import com.flanksource.telltale.observer.ObserverSubscription;
import com.zaxxer.hikari.HikariConfig;
import com.zaxxer.hikari.HikariDataSource;
import java.util.*;
import javax.persistence.*;
import net.bytebuddy.agent.ByteBuddyAgent;
import org.junit.Test;
import org.springframework.orm.jpa.EntityManagerFactoryUtils;
import org.springframework.orm.jpa.JpaTransactionManager;
import org.springframework.orm.jpa.LocalContainerEntityManagerFactoryBean;
import org.springframework.orm.jpa.vendor.HibernateJpaVendorAdapter;
import org.springframework.transaction.support.TransactionTemplate;

public class SpringJpaObserverTest {
  @Test public void observesAnExistingHibernateEntityManagerFactoryAndSpringTransaction() throws Exception {
    HikariConfig config = new HikariConfig();
    config.setJdbcUrl("jdbc:h2:mem:spring-jpa-" + UUID.randomUUID());
    config.setMaximumPoolSize(1);
    try (HikariDataSource pool = new HikariDataSource(config)) {
      LocalContainerEntityManagerFactoryBean bean = new LocalContainerEntityManagerFactoryBean();
      bean.setDataSource(pool);
      bean.setPackagesToScan(SpringJpaObserverTest.class.getPackage().getName());
      bean.setJpaVendorAdapter(new HibernateJpaVendorAdapter());
      bean.setJpaPropertyMap(Collections.singletonMap("hibernate.hbm2ddl.auto", "create-drop"));
      bean.afterPropertiesSet();
      EntityManagerFactory factory = bean.getObject();
      assertNotNull(factory);
      try {
        EntityManager warm = factory.createEntityManager();
        warm.getTransaction().begin();
        warm.persist(new Order(7L, "private-order-value"));
        warm.getTransaction().commit();
        warm.close();
        TransactionTemplate transaction = new TransactionTemplate(new JpaTransactionManager(factory));
        ObserverRuntime.install(ByteBuddyAgent.install());
        try (ObserverSubscription capture = ObserverRuntime.subscribe("sql", "spring-jpa", Collections.singletonMap("connections", Collections.singletonList("*")))) {
          transaction.execute(status -> {
            EntityManager entityManager = EntityManagerFactoryUtils.getTransactionalEntityManager(factory);
            assertNotNull(entityManager);
            Order order = entityManager.find(Order.class, 7L);
            assertEquals("private-order-value", order.payload);
            order.payload = "private-updated-value";
            return null;
          });
          List<Map<String, Object>> rows = capture.drain();
          List<Map<String, Object>> orders = new ArrayList<>();
          for (Map<String, Object> row : rows) if (String.valueOf(row.get("sql")).toLowerCase(Locale.ROOT).contains("spring_jpa_orders")) orders.add(row);
          assertEquals("Hibernate select and flush update must each be recorded once", 2, orders.size());
          for (Map<String, Object> row : orders) {
            assertEquals("jdbc", row.get("api"));
            assertNotNull(row.get("operationId"));
            assertFalse(row.containsKey("error"));
            assertFalse(row.toString().contains("private-order-value"));
            assertFalse(row.toString().contains("private-updated-value"));
          }
        }
        EntityManager verify = factory.createEntityManager();
        assertEquals("private-updated-value", verify.find(Order.class, 7L).payload);
        verify.close();
      } finally { bean.destroy(); }
    }
  }

  @Entity(name = "ObserverOrder")
  @Table(name = "spring_jpa_orders")
  public static class Order {
    @Id public Long id;
    public String payload;
    public Order() {}
    Order(Long id, String payload) { this.id = id; this.payload = payload; }
  }
}
