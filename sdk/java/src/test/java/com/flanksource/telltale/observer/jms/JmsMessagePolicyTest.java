package com.flanksource.telltale.observer.jms;

import static org.junit.Assert.*;
import java.util.*;
import org.junit.Test;

public class JmsMessagePolicyTest {
  @Test public void failedRequestReplyWithoutMessageProducesNoEvent() {
    List<Map<String,Object>> rows = new ArrayList<>();
    JmsObserver observer = new JmsObserver(rows::add, () -> 0);
    Object exchange = observer.onEnter("org.springframework.jms.core.JmsTemplate", "convertSendAndReceive", "()Ljava/lang/Object;", this, new Object[0]);
    observer.onExit(exchange, null, new IllegalArgumentException("conversion rejected"));
    assertTrue(rows.isEmpty());
  }

  @Test public void nestedReceiveAndFailedHandlerProduceOneDelivery() {
    List<Map<String,Object>> rows = new ArrayList<>();
    JmsObserver observer = new JmsObserver(rows::add, () -> 0);
    javax.jms.Message message = (javax.jms.Message) java.lang.reflect.Proxy.newProxyInstance(
        getClass().getClassLoader(), new Class<?>[]{javax.jms.Message.class}, (proxy, method, args) -> null);
    Object outer = observer.onEnter("org.springframework.jms.listener.DefaultMessageListenerContainer", "receiveAndExecute", "()Z", this, new Object[0]);
    Object receive = observer.onEnter("javax.jms.MessageConsumer", "receive", "()Ljavax/jms/Message;", this, new Object[0]);
    observer.onExit(receive,message,null);
    Object handler = observer.onEnter("javax.jms.MessageListener","onMessage","(Ljavax/jms/Message;)V",this,new Object[]{message});
    observer.onExit(handler,null,new IllegalStateException("handler rejected order"));
    observer.onExit(outer,true,null);
    assertEquals(1,rows.size());
    assertEquals("process",rows.get(0).get("operation"));
    assertEquals("error",rows.get(0).get("outcome"));
    assertEquals("handler",rows.get(0).get("phase"));
    assertNotNull(rows.get(0).get("deliveryId"));
  }

  @Test public void emptyPollsAndUnfinishedWaitsDoNotBecomeMessages() {
    JmsOperation operation = new JmsOperation("receive");
    assertNull(operation.incomplete());
    assertNull(operation.finish(null, "timeout", 0));
  }

  @Test public void handlerMappingsRemainIdleAfterProcessing() {
    JmsObserver observer = new JmsObserver(row -> {}, () -> 0);
    assertTrue(observer.kinds().contains("jms_top"));
    javax.jms.Message message = (javax.jms.Message) java.lang.reflect.Proxy.newProxyInstance(
        getClass().getClassLoader(), new Class<?>[]{javax.jms.Message.class}, (proxy, method, args) -> null);
    Object call = observer.onEnter("javax.jms.MessageListener", "onMessage", "(Ljavax/jms/Message;)V", this, new Object[]{message});
    assertEquals(1, observer.snapshot("jms_top").size());
    observer.onExit(call, null, null);
    List<Map<String,Object>> idle = observer.snapshot("jms_top");
    assertEquals(1, idle.size());
    assertEquals("idle", ((Map<?,?>) idle.get(0).get("attributes")).get("state"));
  }
}
