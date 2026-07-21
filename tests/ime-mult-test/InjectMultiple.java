import java.lang.reflect.*;

/** Reflect InputManager.injectInputEvent with a KeyEvent carrying Unicode characters.
 *  Tests whether ACTION_MULTIPLE / KEYCODE_UNKNOWN+characters is still consumed on this OS.
 *  Run: CLASSPATH=/data/local/tmp/inj.jar app_process / InjectMultiple "你好ABC" */
public class InjectMultiple {
    static void p(String s) { System.out.println(s); }

    static Object buildKeyEvent(Constructor<?> charCtor, String text, int action, int keyCode) {
        Class<?>[] ps = charCtor.getParameterTypes();
        Object[] a = new Object[ps.length];
        int intIdx = 0;
        for (int i = 0; i < ps.length; i++) {
            if (ps[i] == long.class) a[i] = 0L;
            else if (ps[i] == int.class) {
                if (intIdx == 0) a[i] = action;
                else if (intIdx == 1) a[i] = keyCode;
                else a[i] = 0;
                intIdx++;
            } else if (ps[i] == String.class) a[i] = text;
            else a[i] = null;
        }
        try { return charCtor.newInstance(a); } catch (Throwable t) { p("newInstance err: " + t); return null; }
    }

    public static void main(String[] args) throws Exception {
        String text = (args.length > 0) ? args[0] : "你好ABC";
        p("payload = \"" + text + "\" (len=" + text.length()
                + " codepoints=" + text.codePointCount(0, text.length()) + ")");

        // ── 0. prepare Looper (InputManager may need it) ──
        try {
            Class<?> looper = Class.forName("android.os.Looper");
            looper.getMethod("prepareMainLooper").invoke(null);
            p("Looper.prepareMainLooper() OK");
        } catch (Throwable t) { p("looper: " + t); }

        // ── 1. get InputManager ──
        Class<?> imc = Class.forName("android.hardware.input.InputManager");
        Object im = null;

        // approach a: getInstance()
        try {
            Method g = imc.getDeclaredMethod("getInstance");
            g.setAccessible(true);
            im = g.invoke(null);
            p("got IM via getInstance: " + im);
        } catch (Throwable t) {
            p("getInstance() err: " + t);
            if (t instanceof InvocationTargetException) p("  cause: " + ((InvocationTargetException)t).getCause());
        }

        // approach b: getInstance(Looper)
        if (im == null) {
            try {
                Method g = imc.getDeclaredMethod("getInstance", Class.forName("android.os.Looper"));
                g.setAccessible(true);
                Object lo = Class.forName("android.os.Looper").getMethod("getMainLooper").invoke(null);
                im = g.invoke(null, lo);
                p("got IM via getInstance(Looper): " + im);
            } catch (Throwable t) {
                p("getInstance(Looper) err: " + t);
                if (t instanceof InvocationTargetException) p("  cause: " + ((InvocationTargetException)t).getCause());
            }
        }

        // approach c: via ServiceManager → IInputManager.getInputManager?
        // or just use IInputManager.injectInputEvent directly
        if (im == null) {
            try {
                Class<?> sm = Class.forName("android.os.ServiceManager");
                Object binder = sm.getMethod("getService", String.class).invoke(null, "input");
                Class<?> stub = Class.forName("android.hardware.input.IInputManager$Stub");
                Object iim = stub.getMethod("asInterface", Class.forName("android.os.IBinder")).invoke(null, binder);
                p("got IInputManager binder: " + iim);
                // Try to get InputManager instance via IInputManager.getInputManager? No.
                // Alternatively, call injectInputEvent on IInputManager directly
                try {
                    Class<?> iimIface = Class.forName("android.hardware.input.IInputManager");
                    Method inj = iimIface.getMethod("injectInputEvent",
                            Class.forName("android.view.InputEvent"), int.class);
                    // use iim as proxy
                    im = iim; // treat IInputManager as our injection proxy
                } catch (Throwable tt) { p("IInputManager inject check: " + tt); }
            } catch (Throwable t) { p("binder approach err: " + t); }
        }

        if (im == null) { p("FATAL: no InputManager"); return; }
        p("using injection target: " + im + " [" + im.getClass().getName() + "]");

        // ── 2. find KeyEvent chars constructor ──
        Class<?> ke = Class.forName("android.view.KeyEvent");
        Constructor<?> charCtor = null;
        for (Constructor<?> c : ke.getDeclaredConstructors()) {
            Class<?>[] ps = c.getParameterTypes();
            if (ps.length > 0 && ps[ps.length - 1] == String.class) {
                c.setAccessible(true);
                if (charCtor == null || ps.length > charCtor.getParameterTypes().length) charCtor = c;
            }
        }
        p("characters-ctor: " + charCtor);

        // ── 3. inject ──
        final int WAIT_FOR_RESULT = 1;
        final int ACTION_MULTIPLE = 2, ACTION_DOWN = 0, KEYCODE_UNKNOWN = 0;

        Method getChars = ke.getMethod("getCharacters");
        Method getAction = ke.getMethod("getAction");
        Method getKeyCode = ke.getMethod("getKeyCode");

        // Find inject method on the actual class we got
        Method inject = null;
        for (Method m : im.getClass().getMethods()) {
            if (m.getName().equals("injectInputEvent")
                    && m.getParameterTypes().length == 2
                    && m.getParameterTypes()[0].getName().contains("InputEvent")) {
                inject = m;
                break;
            }
        }
        if (inject == null) {
            // try declared methods
            for (Method m : im.getClass().getDeclaredMethods()) {
                if (m.getName().equals("injectInputEvent")
                        && m.getParameterTypes().length == 2) {
                    m.setAccessible(true);
                    inject = m;
                    break;
                }
            }
        }
        p("inject method: " + inject);

        if (inject == null) { p("FATAL: no injectInputEvent method"); return; }

        for (int action : new int[]{ACTION_MULTIPLE, ACTION_DOWN}) {
            String label = (action == ACTION_MULTIPLE) ? "ACTION_MULTIPLE" : "ACTION_DOWN";
            Object ev = buildKeyEvent(charCtor, text, action, KEYCODE_UNKNOWN);
            if (ev == null) continue;
            p("\n--- injecting " + label + " + KEYCODE_UNKNOWN + characters ---");
            p("  getAction=" + getAction.invoke(ev) + " getKeyCode=" + getKeyCode.invoke(ev)
                    + " getCharacters=\"" + getChars.invoke(ev) + "\"");
            try {
                Object res = inject.invoke(im, ev, WAIT_FOR_RESULT);
                p("  injectInputEvent result = " + res);
            } catch (InvocationTargetException ite) {
                p("  injectInputEvent THREW: " + ite.getCause());
                if (ite.getCause() != null) ite.getCause().printStackTrace(System.out);
            }
        }
        p("\n(done — inspect logcat -s IMETEST for TextWatcher activity)");
    }
}
