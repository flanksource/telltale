public class Workload {
    public static void main(String[] args) throws Exception {
        System.out.println("ready");
        long seconds=args.length==0?60:Long.parseLong(args[0]);
        long end=System.nanoTime()+seconds*1_000_000_000L;
        long value=0;
        while(System.nanoTime()<end) { value+=Math.sqrt(value+1); }
        System.out.println(value);
    }
}
