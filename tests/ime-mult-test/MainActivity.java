package com.imetest.app;

import android.app.Activity;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.text.Editable;
import android.text.InputType;
import android.text.TextWatcher;
import android.util.Log;
import android.view.inputmethod.InputMethodManager;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.hardware.input.InputManager;
import android.view.InputEvent;
import android.view.KeyEvent;
import java.lang.reflect.*;

/** EditText with TextWatcher + self-injects ACTION_MULTIPLE KeyEvent to test if OS still consumes it. */
public class MainActivity extends Activity {
    static final String T = "IMETEST";
    EditText et;

    @Override protected void onCreate(Bundle b) {
        super.onCreate(b);
        LinearLayout ll = new LinearLayout(this);
        ll.setOrientation(LinearLayout.VERTICAL);
        et = new EditText(this);
        et.setInputType(InputType.TYPE_CLASS_TEXT);
        et.setHint("target field");
        ll.addView(et);
        setContentView(ll);

        et.addTextChangedListener(new TextWatcher() {
            public void beforeTextChanged(CharSequence s, int a, int c, int d) {
                Log.i(T, "before: \"" + s + "\" start=" + a + " count=" + c + " after=" + d);
            }
            public void onTextChanged(CharSequence s, int a, int bb, int c) {
                Log.i(T, "onText:   \"" + s + "\" start=" + a + " before=" + bb + " count=" + c);
            }
            public void afterTextChanged(Editable e) {
                Log.i(T, "after:    \"" + e + "\" len=" + e.length());
            }
        });

        et.requestFocus();
        InputMethodManager imm = (InputMethodManager) getSystemService(INPUT_METHOD_SERVICE);
        imm.showSoftInput(et, 0);
        Log.i(T, "=== EditText focused, IME shown ===");

        // Inject after IME has time to bind
        new Handler(Looper.getMainLooper()).postDelayed(this::runInject, 2000);
    }

    void p(String s) { Log.i(T, s); }

    /** Build a KeyEvent with ACTION_MULTIPLE + KEYCODE_UNKNOWN + characters via reflection
     *  (the public KeyEvent(int,int) ctor doesn't set characters; need the hidden 11-arg ctor). */
    KeyEvent buildMultiple(String chars) {
        try {
            // KeyEvent(long downTime, long eventTime, int action, int keyCode,
            //   int repeat, int metaState, int deviceId, int scancode, int flags,
            //   long source, String characters) — @hide
            Constructor<?>[] ctors = KeyEvent.class.getDeclaredConstructors();
            Constructor<?> charCtor = null;
            for (Constructor<?> c : ctors) {
                Class<?>[] ps = c.getParameterTypes();
                if (ps.length > 0 && ps[ps.length - 1] == String.class) {
                    c.setAccessible(true);
                    if (charCtor == null || ps.length > charCtor.getParameterTypes().length) charCtor = c;
                }
            }
            if (charCtor == null) { p("no char-ctor found"); return null; }
            Object[] args = fillArgs(charCtor.getParameterTypes(), chars, 2, 0); // action=2, keyCode=0
            return (KeyEvent) charCtor.newInstance(args);
        } catch (Throwable t) { p("buildMultiple err: " + t); return null; }
    }

    KeyEvent buildDown(String chars) {
        try {
            Constructor<?>[] ctors = KeyEvent.class.getDeclaredConstructors();
            Constructor<?> charCtor = null;
            for (Constructor<?> c : ctors) {
                Class<?>[] ps = c.getParameterTypes();
                if (ps.length > 0 && ps[ps.length - 1] == String.class) {
                    c.setAccessible(true);
                    if (charCtor == null || ps.length > charCtor.getParameterTypes().length) charCtor = c;
                }
            }
            if (charCtor == null) { p("no char-ctor found"); return null; }
            Object[] args = fillArgs(charCtor.getParameterTypes(), chars, 0, 0); // action=0, keyCode=0
            return (KeyEvent) charCtor.newInstance(args);
        } catch (Throwable t) { p("buildDown err: " + t); return null; }
    }

    Object[] fillArgs(Class<?>[] ps, String chars, int action, int keyCode) {
        Object[] a = new Object[ps.length];
        int intIdx = 0;
        for (int i = 0; i < ps.length; i++) {
            if (ps[i] == long.class) a[i] = 0L;
            else if (ps[i] == int.class) {
                if (intIdx == 0) a[i] = action;
                else if (intIdx == 1) a[i] = keyCode;
                else a[i] = 0;
                intIdx++;
            } else if (ps[i] == String.class) a[i] = chars;
            else a[i] = null;
        }
        return a;
    }

    void runInject() {
        p("=== starting injection ===");
        InputManager im = (InputManager) getSystemService(INPUT_SERVICE);
        p("InputManager: " + im);

        // injectInputEvent is @hide — reflect
        Method inject = null;
        try {
            inject = InputManager.class.getDeclaredMethod("injectInputEvent", InputEvent.class, int.class);
            inject.setAccessible(true);
        } catch (Throwable t) { p("no injectInputEvent: " + t); return; }

        final int WAIT = 1; // INJECT_INPUT_EVENT_MODE_WAIT_FOR_RESULT
        String text = "你好ABC";

        // 1. try ACTION_MULTIPLE
        KeyEvent ev1 = buildMultiple(text);
        if (ev1 != null) {
            p("injecting ACTION_MULTIPLE: action=" + ev1.getAction()
                    + " keyCode=" + ev1.getKeyCode()
                    + " chars=\"" + ev1.getCharacters() + "\"");
            try {
                Object res = inject.invoke(im, ev1, WAIT);
                p("ACTION_MULTIPLE inject result = " + res);
            } catch (Throwable t) {
                p("ACTION_MULTIPLE inject err: " + t);
                if (t.getCause() != null) Log.e(T, "cause", t.getCause());
            }
        }

        // 2. try ACTION_DOWN + KEYCODE_UNKNOWN + characters
        KeyEvent ev2 = buildDown(text);
        if (ev2 != null) {
            p("injecting ACTION_DOWN+UNKNOWN: action=" + ev2.getAction()
                    + " keyCode=" + ev2.getKeyCode()
                    + " chars=\"" + ev2.getCharacters() + "\"");
            try {
                Object res = inject.invoke(im, ev2, WAIT);
                p("ACTION_DOWN+UNKNOWN inject result = " + res);
            } catch (Throwable t) {
                p("ACTION_DOWN+UNKNOWN inject err: " + t);
                if (t.getCause() != null) Log.e(T, "cause", t.getCause());
            }
        }

        // 3. control: inject regular key event (ASCII char) to confirm injection works at all
        p("control: injecting KeyEvent KEYCODE_A to verify inject path works");
        KeyEvent ctrl = new KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_A);
        try {
            Object res = inject.invoke(im, ctrl, WAIT);
            p("control KEYCODE_A inject result = " + res);
            inject.invoke(im, new KeyEvent(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_A), WAIT);
            p("control KEYCODE_A UP injected");
        } catch (Throwable t) {
            p("control inject err: " + t);
        }

        p("=== injection done, TextWatcher results above ===");
    }
}
