import android.content.ClipData;
import android.os.IBinder;
import java.lang.reflect.Method;

/** Dev probe run as the shell user: can it READ the clipboard while no app has focus? */
public class GetClip {
    public static void main(String[] args) throws Exception {
        Class<?> sm = Class.forName("android.os.ServiceManager");
        IBinder b = (IBinder) sm.getMethod("getService", String.class).invoke(null, "clipboard");
        Class<?> stub = Class.forName("android.content.IClipboard$Stub");
        Object svc = stub.getMethod("asInterface", IBinder.class).invoke(null, b);
        for (Method m : svc.getClass().getMethods()) {
            if (!m.getName().equals("getPrimaryClip")) continue;
            Class<?>[] p = m.getParameterTypes();
            Object[] a = new Object[p.length];
            for (int i = 0; i < p.length; i++) {
                if (p[i] == String.class) a[i] = (i == 0) ? "com.android.shell" : null;
                else if (p[i] == int.class) a[i] = 0;
            }
            Object r = m.invoke(svc, a);
            if (r == null) { System.out.println("getPrimaryClip -> null (denied or empty)"); return; }
            ClipData c = (ClipData) r;
            System.out.println("getPrimaryClip -> items=" + c.getItemCount() + " text=" + c.getItemAt(0).getText());
            return;
        }
        System.out.println("no getPrimaryClip found");
    }
}
