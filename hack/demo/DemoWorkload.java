import java.util.concurrent.locks.LockSupport;

public class DemoWorkload {
    private static volatile long sink;
    private static final Object lock = new Object();

    public static void main(String[] args) throws Exception {
        Runnable[] jobs = {
            () -> { while (true) { for (int i = 0; i < 100_000; i++) sink += i; LockSupport.parkNanos(1_000_000); } },
            () -> { while (true) { byte[] allocation = new byte[1024 * 1024]; sink = allocation.length; LockSupport.parkNanos(10_000_000); } },
            () -> { while (true) { synchronized (lock) { LockSupport.parkNanos(30_000_000); } } },
            () -> { while (true) { synchronized (lock) { sink++; } LockSupport.parkNanos(10_000_000); } }
        };
        for (Runnable job : jobs) {
            Thread thread = new Thread(job);
            thread.setDaemon(true);
            thread.start();
        }
        System.out.println("Demo workload ready");
        System.in.read();
    }
}
