import android.content.ClipData;
import android.os.IBinder;
import java.lang.reflect.Method;

/** Dev helper run as the shell user (app_process): copies a text to the clipboard like "another app". */
public class SetClip {
    public static void main(String[] args) throws Exception {
        String text = args.length > 0 ? args[0] : "clipsilent-test";
        Class<?> sm = Class.forName("android.os.ServiceManager");
        IBinder b = (IBinder) sm.getMethod("getService", String.class).invoke(null, "clipboard");
        Class<?> stub = Class.forName("android.content.IClipboard$Stub");
        Object svc = stub.getMethod("asInterface", IBinder.class).invoke(null, b);
        ClipData clip = ClipData.newPlainText("dev", text);
        for (Method m : svc.getClass().getMethods()) {
            if (!m.getName().equals("setPrimaryClip")) continue;
            Class<?>[] p = m.getParameterTypes();
            Object[] a = new Object[p.length];
            a[0] = clip;
            int ints = 0;
            for (int i = 1; i < p.length; i++) {
                if (p[i] == String.class) a[i] = (i == 1) ? "com.android.shell" : null;
                else if (p[i] == int.class) { a[i] = 0; ints++; }
            }
            m.invoke(svc, a);
            System.out.println("setPrimaryClip ok (" + p.length + " params)");
            return;
        }
        System.out.println("no setPrimaryClip found");
    }
}
