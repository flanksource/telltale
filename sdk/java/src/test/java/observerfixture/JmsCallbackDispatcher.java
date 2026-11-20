package observerfixture;

public class JmsCallbackDispatcher {
  public void deliver(javax.jms.MessageListener listener, javax.jms.Message message) {
    listener.onMessage(message);
  }
}
