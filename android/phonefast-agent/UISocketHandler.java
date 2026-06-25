package com.genymobile.scrcpy.control;

import android.app.UiAutomation;
import android.os.Build;
import android.os.Looper;
import android.util.JsonWriter;
import android.view.accessibility.AccessibilityNodeInfo;
import android.view.accessibility.AccessibilityWindowInfo;

import com.genymobile.scrcpy.util.Ln;

import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.io.IOException;
import java.io.StringWriter;
import java.lang.reflect.Constructor;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.List;

import android.net.LocalServerSocket;
import android.net.LocalSocket;

/**
 * Handles fast UI hierarchy dump requests over a dedicated local socket.
 *
 * Protocol:
 *   Request:  "dump\0" (5 bytes)
 *   Response: 4-byte big-endian length + JSON bytes
 *
 * UiAutomation is initialised inside the phonefast-ui thread via reflection
 * (same mechanism used by "uiautomator dump"), so no Instrumentation is needed.
 */
public final class UISocketHandler {

    private static final String UI_SOCKET_SUFFIX = "_ui";
    private static final byte[] DUMP_REQUEST = "dump\0".getBytes(StandardCharsets.US_ASCII);
    // Maximum elements collected per dump (avoids OOM on complex screens)
    private static final int MAX_ELEMENTS = 500;

    private final int scid;
    private volatile boolean running = true;
    private volatile UiAutomation uiAutomation;

    public UISocketHandler(int scid) {
        this.scid = scid;
    }

    public void start() {
        String socketName = getSocketName();
        Ln.i("phonefast: UISocketHandler starting on " + socketName);

        new Thread(() -> {
            // ── Step 1: prepare Looper for this thread ───────────────────────
            // UiAutomation.connect() throws if Looper.myLooper() == null.
            if (Looper.myLooper() == null) {
                Looper.prepare();
            }

            // ── Step 2: create UiAutomation via reflection ────────────────────
            // This mirrors what "uiautomator dump" does: instantiates
            // android.app.UiAutomationConnection (internal class) and passes it
            // to UiAutomation's hidden constructor, then calls connect().
            // Shell UID (2000) has android.permission.RETRIEVE_WINDOW_CONTENT.
            try {
                Class<?> connClass = Class.forName("android.app.UiAutomationConnection");
                Object conn = connClass.getConstructor().newInstance();

                Class<?> iConnClass = Class.forName("android.app.IUiAutomationConnection");
                Constructor<?> ctor = UiAutomation.class.getDeclaredConstructor(
                        Looper.class, iConnClass);
                ctor.setAccessible(true);

                UiAutomation ua = (UiAutomation) ctor.newInstance(Looper.myLooper(), conn);
                // connect() is @hide — must call via reflection
                ua.getClass().getDeclaredMethod("connect").invoke(ua);
                uiAutomation = ua;
                Ln.i("phonefast: UiAutomation connected");
            } catch (Exception e) {
                Ln.w("phonefast: UiAutomation init failed (" + e.getClass().getSimpleName()
                        + "): " + e.getMessage());
                uiAutomation = null;
            }

            // ── Step 3: accept loop ───────────────────────────────────────────
            try {
                LocalServerSocket serverSocket = new LocalServerSocket(socketName);
                Ln.i("phonefast: UI socket ready on " + socketName);

                while (running) {
                    try (LocalSocket client = serverSocket.accept()) {
                        handleClient(client);
                    } catch (IOException e) {
                        if (running) {
                            Ln.w("phonefast: UI accept error: " + e.getMessage());
                        }
                    }
                }
                serverSocket.close();
            } catch (IOException e) {
                Ln.e("phonefast: UI socket server error: " + e.getMessage());
            }
        }, "phonefast-ui").start();
    }

    public void stop() {
        running = false;
        UiAutomation ua = uiAutomation;
        if (ua != null) {
            try {
                // disconnect() is @hide — must call via reflection
                ua.getClass().getDeclaredMethod("disconnect").invoke(ua);
            } catch (Exception ignore) {
                // best-effort
            }
            uiAutomation = null;
        }
    }

    // ── helpers ────────────────────────────────────────────────────────────────

    private String getSocketName() {
        if (scid == -1) return "scrcpy" + UI_SOCKET_SUFFIX;
        return "scrcpy_" + String.format("%08x", scid) + UI_SOCKET_SUFFIX;
    }

    private void handleClient(LocalSocket socket) {
        try {
            DataInputStream in = new DataInputStream(socket.getInputStream());
            DataOutputStream out = new DataOutputStream(socket.getOutputStream());

            byte[] req = new byte[5];
            in.readFully(req);

            if (Arrays.equals(req, DUMP_REQUEST)) {
                byte[] json = dumpUIHierarchy();
                out.writeInt(json.length);
                out.write(json);
                out.flush();
            } else {
                Ln.w("phonefast: unknown UI request");
            }
        } catch (IOException e) {
            // socket closed by client or timeout — not an error
        }
    }

    // ── dump ───────────────────────────────────────────────────────────────────

    private byte[] dumpUIHierarchy() {
        UiAutomation ua = uiAutomation;
        if (ua == null) {
            return buildError("UiAutomation not available");
        }

        StringWriter sw = new StringWriter(8192);
        try {
            JsonWriter jw = new JsonWriter(sw);
            jw.beginObject();
            jw.name("elements");
            jw.beginArray();

            int[] counter = {0};
            // Try all windows first (gives more complete picture)
            // Iterate in REVERSE order: topmost windows (dialogs, sheets)
            // come last in z-order but should be processed FIRST so they
            // don't get starved by the main window exhausting MAX_ELEMENTS.
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
                try {
                    List<AccessibilityWindowInfo> windows = ua.getWindows();
                    if (windows != null) {
                        for (int i = windows.size() - 1; i >= 0; i--) {
                            if (counter[0] >= MAX_ELEMENTS) break;
                            AccessibilityNodeInfo root = windows.get(i).getRoot();
                            if (root != null) {
                                collectNodes(root, jw, counter);
                            }
                        }
                    }
                } catch (Exception e) {
                    Ln.w("phonefast: getWindows failed, falling back: " + e.getMessage());
                    // fall through to getRootInActiveWindow
                }
            }

            // Fallback or supplement: active window root
            if (counter[0] == 0) {
                AccessibilityNodeInfo root = ua.getRootInActiveWindow();
                if (root != null) {
                    collectNodes(root, jw, counter);
                }
            }

            jw.endArray();
            jw.endObject();
            jw.close();
            return sw.toString().getBytes(StandardCharsets.UTF_8);

        } catch (Exception e) {
            return buildError(e.getMessage());
        }
    }

    /**
     * Recursively collect nodes into a flat JSON array.
     * Each element is phone-mcp compatible (index, text, bounds, etc.).
     */
    private void collectNodes(AccessibilityNodeInfo node, JsonWriter jw, int[] counter)
            throws IOException {
        if (node == null || counter[0] >= MAX_ELEMENTS) return;

        android.graphics.Rect rect = new android.graphics.Rect();
        node.getBoundsInScreen(rect);

        // Skip zero-size elements
        if (rect.width() > 0 || rect.height() > 0) {
            CharSequence text = node.getText();
            CharSequence desc = node.getContentDescription();
            String resId = node.getViewIdResourceName();
            CharSequence cls = node.getClassName();

            // Only emit elements that have useful attributes
            boolean hasText = text != null && text.length() > 0;
            boolean hasDesc = desc != null && desc.length() > 0;
            boolean hasResId = resId != null && !resId.isEmpty();
            boolean clickable = node.isClickable();

            if (hasText || hasDesc || hasResId || clickable) {
                jw.beginObject();
                jw.name("index").value(counter[0]++);
                jw.name("text").value(text != null ? text.toString() : "");
                jw.name("content_desc").value(desc != null ? desc.toString() : "");
                jw.name("resource_id").value(resId != null ? resId : "");
                jw.name("class_name").value(cls != null ? cls.toString() : "");

                jw.name("bounds");
                jw.beginArray();
                jw.value(rect.left); jw.value(rect.top);
                jw.value(rect.right); jw.value(rect.bottom);
                jw.endArray();

                jw.name("center");
                jw.beginArray();
                jw.value((rect.left + rect.right) / 2);
                jw.value((rect.top + rect.bottom) / 2);
                jw.endArray();

                jw.name("clickable").value(clickable);
                jw.name("enabled").value(node.isEnabled());
                jw.endObject();
            }
        }

        // Recurse into children
        int childCount = node.getChildCount();
        for (int i = 0; i < childCount; i++) {
            if (counter[0] >= MAX_ELEMENTS) break;
            AccessibilityNodeInfo child = node.getChild(i);
            if (child != null) {
                collectNodes(child, jw, counter);
            }
        }
    }

    private static byte[] buildError(String msg) {
        String s = "{\"elements\":[],\"error\":\"" + (msg != null ? msg : "unknown") + "\"}";
        return s.getBytes(StandardCharsets.UTF_8);
    }
}
