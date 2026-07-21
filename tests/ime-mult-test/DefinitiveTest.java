import java.lang.reflect.*;
import java.util.ArrayList;

/**
 * Definitive test: ACTION_MULTIPLE + KEYCODE_UNKNOWN + characters via injectInputEvent.
 * Uses scrcpy's Context→getSystemService("input")→InputManager pattern.
 * Prerequisite: com.imetest.app/.MainActivity running with focused EditText.
 * Run: CLASSPATH=/data/local/tmp/deftest.jar app_process / DefinitiveTest "你好测试"
 */
public class DefinitiveTest {
    static ArrayList<String> log = new ArrayList<>();
    static void p(String s) { System.out.println(s); log.add(s); }

    // Standard KeyEvent(10-or-11-arg) mapping: (long,long,int,int,int,int,int,int,int,long[,String])
    static boolean isStandardCtor(Class<?>[] ps) {
        return ps.length >= 10
                && ps[0] == long.class
                && ps[1] == long.class
                && ps[2] == int.class
                && ps[3] == int.class
                && ps[4] == int.class
                && ps[9] == long.class;
    }

    // Fill the 10-or-11-arg standard KeyEvent constructor
    static Object[] fillStandard(Class<?>[] ps, long time, int action, int keyCode, String chars) {
        Object[] a = new Object[ps.length];
        // index 0-1: long downTime/eventTime
        a[0] = time;  a[1] = time;
        // index 2: int action
        a[2] = action;
        // index 3: int keyCode
        a[3] = keyCode;
        // index 4-8: int repeat/metastate/deviceId/scancode/flags -> 0
        for (int i = 4; i <= 8; i++) a[i] = 0;
        // index 9: long source (KEYBOARD=0x101)
        a[9] = 0x101L;
        // index 10 (if exists): String characters
        if (ps.length > 10 && ps[10] == String.class) a[10] = chars;
        return a;
    }

    public static void main(String[] args) throws Exception {
        String text = (args.length > 0) ? args[0] : "你好测试ABC";
        p("=== Definitive ACTION_MULTIPLE test ===");
        p("payload: \"" + text + "\" len=" + text.length()
                + " codepoints=" + text.codePointCount(0, text.length()));
        // escape non-ASCII for visibility
        StringBuilder esc = new StringBuilder();
        for (int i = 0; i < text.length(); i++) {
            char c = text.charAt(i);
            if (c > 127) esc.append("\\u").append(String.format("%04x", (int) c));
            else esc.append(c);
        }
        p("payload(escaped): " + esc);

        // ── 0. Looper ──
        Class<?> looperCls = Class.forName("android.os.Looper");
        looperCls.getMethod("prepareMainLooper").invoke(null);
        p("Looper OK");

        // ── 1. System context (scrcpy pattern) ──
        Class<?> atCls = Class.forName("android.app.ActivityThread");
        Object thread = atCls.getMethod("systemMain").invoke(null);
        Object context = atCls.getMethod("getSystemContext").invoke(thread);
        p("systemContext: " + context.getClass().getName());

        // ── 2. InputManager via getSystemService("input") ──
        Object inputManager = context.getClass()
                .getMethod("getSystemService", String.class)
                .invoke(context, "input");
        p("InputManager: " + (inputManager != null ? inputManager.getClass().getName() : "NULL"));
        if (inputManager == null) { p("FATAL"); return; }

        // ── 3. reflect injectInputEvent ──
        // Try android.hardware.input.InputManager (public, hidden method)
        Class<?> imCls = Class.forName("android.hardware.input.InputManager");
        Method injectMethod = imCls.getMethod("injectInputEvent",
                Class.forName("android.view.InputEvent"), int.class);
        p("injectMethod: " + injectMethod);

        // ── 4. Dump ALL KeyEvent constructors ──
        Class<?> keCls = Class.forName("android.view.KeyEvent");
        ArrayList<Constructor<?>> standardCharCtors = new ArrayList<>();
        ArrayList<Constructor<?>> otherCharCtors = new ArrayList<>();
        ArrayList<Constructor<?>> standardBasicCtors = new ArrayList<>();

        p("\n=== KeyEvent constructors ===");
        for (Constructor<?> c : keCls.getDeclaredConstructors()) {
            c.setAccessible(true);
            Class<?>[] ps = c.getParameterTypes();
            StringBuilder sb = new StringBuilder("  ");
            sb.append(c.getName()).append("(");
            for (int i = 0; i < ps.length; i++) {
                if (i > 0) sb.append(",");
                sb.append(ps[i].getSimpleName());
            }
            sb.append(")");
            String sig = sb.toString();

            boolean hasChars = ps.length > 0 && ps[ps.length - 1] == String.class;
            boolean standard = isStandardCtor(ps);

            String flag = "";
            if (standard && hasChars) { flag = " ★ STANDARD+CHARS"; standardCharCtors.add(c); }
            else if (standard && !hasChars) { flag = " (standard basic)"; standardBasicCtors.add(c); }
            else if (hasChars) { flag = " (other+chars)"; otherCharCtors.add(c); }
            p(sig + flag);
        }
        p("\nStandard+chars ctors: " + standardCharCtors.size());
        p("Standard basic ctors:   " + standardBasicCtors.size());
        p("Other+chars ctors:      " + otherCharCtors.size());

        // ── 5. TIMING: timestamp for all events ──
        long now = android.os.SystemClock.uptimeMillis();

        // ── 6. CONTROL: inject KEYCODE_A to prove injection works ──
        p("\n=== CONTROL: KEYCODE_A injection ===");
        if (!standardBasicCtors.isEmpty()) {
            Constructor<?> basic = standardBasicCtors.get(0);
            Object[] a = fillStandard(basic.getParameterTypes(), now, 0 /* DOWN */, 29 /* KEYCODE_A */, null);
            Object ev = basic.newInstance(a);
            try {
                Object r = injectMethod.invoke(inputManager, ev, 0); // ASYNC
                p("  KEYCODE_A DOWN ASYNC → " + r);
            } catch (InvocationTargetException ite) {
                p("  KEYCODE_A DOWN THREW: " + ite.getCause().getMessage());
            }
        }

        // ── 7. Test ALL standard+char ctors ──
        int ACTION_MULTIPLE = 2, ACTION_DOWN = 0, KEYCODE_UNKNOWN = 0;
        int[] modes = {0, 1, 2};
        String[] modeNames = {"ASYNC", "WAIT_RESULT", "WAIT_FINISH"};
        int[] actions = {ACTION_MULTIPLE, ACTION_DOWN};
        String[] actionNames = {"ACTION_MULTIPLE(2)","ACTION_DOWN(0)"};

        for (Constructor<?> ctor : standardCharCtors) {
            p("\n=== Testing standard+chars ctor(11-arg) ===");
            Class<?>[] ps = ctor.getParameterTypes();

            for (int action : actions) {
                for (int mi = 0; mi < modes.length; mi++) {
                    String label = actionNames[action == ACTION_MULTIPLE ? 0 : 1]
                            + "+KEYCODE_UNKNOWN mode=" + modeNames[mi];
                    Object[] ca = fillStandard(ps, now, action, KEYCODE_UNKNOWN, text);
                    try {
                        Object ev = ctor.newInstance(ca);
                        // Verify key properties
                        int act = (int) keCls.getMethod("getAction").invoke(ev);
                        int kc = (int) keCls.getMethod("getKeyCode").invoke(ev);
                        String ch = (String) keCls.getMethod("getCharacters").invoke(ev);
                        try {
                            Object r = injectMethod.invoke(inputManager, ev, modes[mi]);
                            p("  " + label + " → inject=" + r
                                    + " event{action=" + act + " code=" + kc
                                    + " chars=" + (ch != null ? ("\""+ch+"\"") : "NULL") + "}");
                        } catch (InvocationTargetException ite) {
                            p("  " + label + " THREW: " + ite.getCause().getMessage());
                        }
                    } catch (Throwable t) {
                        p("  " + label + " BUILD_FAIL: " + t);
                    }
                }
            }
        }

        // ── 8. human-readable verdict ──
        p("\n╔══════════════════════════════════════════╗");
        p("║ Check TextWatcher via:                   ║");
        p("║   adb logcat -d -s IMETEST              ║");
        p("║ Only ASCII from payload ('"+text.replaceAll("[^\\x00-\\x7F]","")+"') should land. ║");
        p("╚══════════════════════════════════════════╝");
    }
}
