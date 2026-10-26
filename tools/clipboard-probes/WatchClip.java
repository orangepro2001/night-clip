import android.content.ClipData;
import android.os.IBinder;
import java.lang.reflect.Method;

/** Dev probe run as the shell user: polls the clipboard and prints every change. */
public class WatchClip {
    public static void main(String[] args) throws Exception {
        Class<?> sm = Class.forName("android.os.ServiceManager");
        IBinder b = (IBinder) sm.getMethod("getService", String.class).invoke(null, "clipboard");
        Class<?> stub = Class.forName("android.content.IClipboard$Stub");
        Object svc = stub.getMethod("asInterface", IBinder.class).invoke(null, b);
        Method get = null;
        for (Method m : svc.getClass().getMethods()) if (m.getName().equals("getPrimaryClip")) get = m;
        Class<?>[] p = get.getParameterTypes();
        long end = System.currentTimeMillis() + Long.parseLong(args.length > 0 ? args[0] : "60000");
        String last = null;
        System.out.println("watching");
        while (System.currentTimeMillis() < end) {
            Object[] a = new Object[p.length];
            for (int i = 0; i < p.length; i++) {
                if (p[i] == String.class) a[i] = (i == 0) ? "com.android.shell" : null;
                else if (p[i] == int.class) a[i] = 0;
            }
            Object r = get.invoke(svc, a);
            String t = r == null ? null : String.valueOf(((ClipData) r).getItemAt(0).getText());
            if (t != null && !t.equals(last)) {
                System.out.println("CHANGED " + System.currentTimeMillis() + " " + t);
                last = t;
            }
            Thread.sleep(300);
        }
    }
}
