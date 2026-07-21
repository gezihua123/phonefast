import java.lang.reflect.*;
import java.util.*;

/**
 * Empirical test: can we reflect InputMethodManager.mCurMethod (IInputMethodSession)
 * and call commitText on it from an automation (app_process) context?
 *
 * Run on device via:
 *   CLASSPATH=/data/local/tmp/imedex.jar app_process / ImeReflect
 */
public class ImeReflect {
    static void p(String s) { System.out.println(s); }

    static Object readField(Object o, String name) {
        Class<?> c = o.getClass();
        while (c != null) {
            try {
                Field f = c.getDeclaredField(name);
                f.setAccessible(true);
                return f.get(o);
            } catch (NoSuchFieldException e) {
                c = c.getSuperclass();
            } catch (Exception e) {
                return "<err: " + e + ">";
            }
        }
        return "<no field>";
    }

    static String sig(Method m) {
        StringBuilder sb = new StringBuilder();
        sb.append(m.getReturnType().getSimpleName()).append(" ");
        sb.append(m.getName()).append("(");
        Class<?>[] ps = m.getParameterTypes();
        for (int i = 0; i < ps.length; i++) {
            if (i > 0) sb.append(",");
            sb.append(ps[i].getSimpleName());
        }
        return sb.append(")").toString();
    }

    /** Enumerate all public methods across class + supers + interfaces. */
    static List<Method> allMethods(Object o) {
        LinkedHashMap<String, Method> out = new LinkedHashMap<>();
        LinkedList<Class<?>> q = new LinkedList<>();
        Set<Class<?>> seen = new HashSet<>();
        q.add(o.getClass());
        while (!q.isEmpty()) {
            Class<?> cur = q.poll();
            if (!seen.add(cur)) continue;
            for (Method m : cur.getMethods()) out.putIfAbsent(sig(m), m);
            if (cur.getSuperclass() != null) q.add(cur.getSuperclass());
            for (Class<?> i : cur.getInterfaces()) q.add(i);
        }
        return new ArrayList<>(out.values());
    }

    public static void main(String[] args) throws Exception {
        // ── 0. prepare main looper (IMM needs one) ──
        try {
            Class<?> looper = Class.forName("android.os.Looper");
            looper.getMethod("prepareMainLooper").invoke(null);
        } catch (Throwable t) { p("looper: " + t); }

        // ── 1. enumerate IInputMethodSession methods (no instance needed) ──
        p("========== IInputMethodSession interface methods ==========");
        String[] candidates = {
            "com.android.internal.inputmethod.IInputMethodSession",  // 13+
            "com.android.internal.view.IInputMethodSession"          // <=12
        };
        Class<?> sessionIface = null;
        for (String n : candidates) {
            try { sessionIface = Class.forName(n); p("loaded: " + n); break; }
            catch (ClassNotFoundException e) {}
        }
        if (sessionIface != null) {
            for (Method m : sessionIface.getMethods()) p("  " + sig(m));
            boolean hasCommit = false;
            for (Method m : sessionIface.getMethods()) {
                if (m.getName().toLowerCase().contains("commit") ||
                    m.getName().toLowerCase().contains("text")) {
                    hasCommit = true; p("  [text-like] " + sig(m));
                }
            }
            if (!hasCommit) p("  >> NO commit/text method on IInputMethodSession");
        } else {
            p("  could not load IInputMethodSession class");
        }

        // ── 2. get InputMethodManager per-process instance ──
        p("\n========== InputMethodManager instance ==========");
        Class<?> immc = Class.forName("android.view.inputmethod.InputMethodManager");
        Object imm = null;
        // try getInstance() no-arg
        try {
            Method g = immc.getDeclaredMethod("getInstance");
            g.setAccessible(true);
            imm = g.invoke(null);
            p("got via getInstance(): " + imm);
        } catch (Throwable t) {
            p("getInstance() failed: " + t);
            // fallback: construct via ServiceManager + IInputMethodManager.Stub.asInterface
            try {
                Class<?> sm = Class.forName("android.os.ServiceManager");
                Method getService = sm.getMethod("getService", String.class);
                Object binder = getService.invoke(null, "input_method");
                Class<?> stub = Class.forName("android.view.inputmethod.IInputMethodManager$Stub");
                Method asIface = stub.getMethod("asInterface", Class.forName("android.os.IBinder"));
                Object service = asIface.invoke(null, binder);
                Class<?> looper = Class.forName("android.os.Looper");
                Object mainLo = looper.getMethod("getMainLooper").invoke(null);
                Constructor<?> ctor = immc.getDeclaredConstructor(
                    Class.forName("android.view.inputmethod.IInputMethodManager"), looper);
                ctor.setAccessible(true);
                imm = ctor.newInstance(service, mainLo);
                p("got via constructor: " + imm);
            } catch (Throwable t2) { p("constructor also failed: " + t2); }
        }
        if (imm == null) { p("FATAL: no IMM instance"); return; }

        // ── 3. dump key fields ──
        p("\n========== IMM fields (this process) ==========");
        String[] fields = {
            "mCurMethod", "mCurClient",
            "mServedInputConnection", "mServedInputConnectionWrapper",
            "mServedView", "mCurrentEditorInfo", "mActive"
        };
        for (String f : fields) {
            Object v = readField(imm, f);
            if (v == null) p(f + " = null");
            else if (v instanceof String) p(f + " = " + v);
            else p(f + " = " + v.getClass().getName() + " @" + Integer.toHexString(System.identityHashCode(v)));
        }

        // ── 4. mCurMethod verdict ──
        Object mCurMethod = readField(imm, "mCurMethod");
        p("\n========== verdict ==========");
        if (mCurMethod == null) {
            p("mCurMethod == null  =>  no active IME session in THIS (app_process) process.");
            p("Reflection commitText from automation process is NOT possible: there is no session object to call.");
        } else if ("<no field>".equals(mCurMethod) || mCurMethod instanceof String) {
            p("mCurMethod field not readable: " + mCurMethod);
        } else {
            p("mCurMethod NON-NULL: " + mCurMethod.getClass().getName());
            p("enumerating callable methods on the live session:");
            for (Method m : allMethods(mCurMethod)) p("  " + sig(m));
            // try commitText variants
            for (Method m : allMethods(mCurMethod)) {
                String n = m.getName().toLowerCase();
                if (n.contains("commit") || (n.contains("text") && !n.contains("get"))) {
                    p("\n  attempting " + sig(m));
                    try {
                        Class<?>[] ps = m.getParameterTypes();
                        Object[] a = new Object[ps.length];
                        for (int i = 0; i < ps.length; i++) {
                            if (ps[i] == int.class || ps[i] == long.class) a[i] = 0;
                            else if (ps[i] == boolean.class) a[i] = false;
                            else a[i] = "测试";
                        }
                        Object r = m.invoke(mCurMethod, a);
                        p("    result=" + r);
                    } catch (Throwable t) { p("    err: " + t); }
                }
            }
        }
        p("\n(done)");
    }
}
