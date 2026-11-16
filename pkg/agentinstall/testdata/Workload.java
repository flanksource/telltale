public class Workload {
    public static void main(String[] args) throws Exception {
        System.out.println("ready");
        long seconds=args.length==0?60:Long.parseLong(args[0]);
        long end=System.nanoTime()+seconds*1_000_000_000L;
        long value=0;
        while(System.nanoTime()<end) { value+=compute(value); }
        System.out.println(value);
    }
    public static double compute(long value) { return Math.sqrt(value+1); }
}
