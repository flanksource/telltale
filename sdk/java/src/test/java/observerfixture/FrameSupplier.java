package observerfixture;

public interface FrameSupplier extends javax.jms.MessageListener {
  default void onMessage(javax.jms.Message message) {}
  long value();
}
