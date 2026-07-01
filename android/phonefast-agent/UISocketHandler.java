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
import java.util.HashSet;
import java.util.List;
import java.util.Set;

import android.net.LocalServerSocket;
import android.net.LocalSocket;

/**
 * Handles fast UI hierarchy dump requests over a dedicated local socket.
 *
 * Protocol:
 *   Request:  "dump\0"           → full mode, default max (500)
 *             "dump:NNN\0"       → full mode, max N elements
 *             "sum\0"            → summary mode, default max (100)
 *             "sum:NNN\0"        → summary mode, max N elements
 *   Response: 4-byte big-endian length + JSON bytes
 *
 * Summary mode filters out pure layout containers (FrameLayout, LinearLayout, etc.)
 * so the result only contains meaningful interactive elements.
 *
 * UiAutomation is initialised inside the phonefast-ui thread via reflection
 * (same mechanism used by "uiautomator dump"), so no Instrumentation is needed.
 */
public final class UISocketHandler {

    private static final String UI_SOCKET_SUFFIX = "_ui";
    // Absolute hard cap — never collect more than this per dump (avoids OOM)
    private static final int ABSOLUTE_MAX_ELEMENTS = 500;
    // Default when no limit specified
    private static final int DEFAULT_MAX_ELEMENTS = 500;
    // Default for summary mode
    private static final int DEFAULT_SUM_ELEMENTS = 100;

    private static final String DUMP_PREFIX = "dump";
    private static final String SUM_PREFIX = "sum";
    private static final byte[] DUMP_PREFIX_BYTES = "dump".getBytes(StandardCharsets.US_ASCII);
    private static final byte[] SUM_PREFIX_BYTES = "sum".getBytes(StandardCharsets.US_ASCII);

    // Layout class names filtered out in summary mode (suffix matching).
    // These are non-interactive containers that just arrange children.
    private static final Set<String> LAYOUT_CLASS_SUFFIXES = new HashSet<>(Arrays.asList(
        "FrameLayout", "LinearLayout", "RelativeLayout", "ConstraintLayout",
        "AbsoluteLayout", "GridLayout", "TableLayout", "TableRow",
        "ScrollView", "HorizontalScrollView", "NestedScrollView",
        "ViewGroup", "ViewStub", "Space", "Spacer",
        "CoordinatorLayout", "DrawerLayout", "SwipeRefreshLayout"
    ));

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
            if (Looper.myLooper() == null) {
                Looper.prepare();
            }

            // ── Step 2: create UiAutomation via reflection ────────────────────
            try {
                Class<?> connClass = Class.forName("android.app.UiAutomationConnection");
                Object conn = connClass.getConstructor().newInstance();

                Class<?> iConnClass = Class.forName("android.app.IUiAutomationConnection");
                Constructor<?> ctor = UiAutomation.class.getDeclaredConstructor(
                        Looper.class, iConnClass);
                ctor.setAccessible(true);

                UiAutomation ua = (UiAutomation) ctor.newInstance(Looper.myLooper(), conn);
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

            // Read until null terminator ('\0')
            byte[] req = readUntilNull(in);
            if (req == null) {
                return;
            }

            // Determine mode: "dump" (full) or "sum" (summary)
            boolean summaryMode;
            if (startsWith(req, DUMP_PREFIX_BYTES)) {
                summaryMode = false;
            } else if (startsWith(req, SUM_PREFIX_BYTES)) {
                summaryMode = true;
            } else {
                Ln.w("phonefast: unknown UI request");
                return;
            }

            int maxElements = parseMaxElements(req, summaryMode);
            byte[] json = dumpUIHierarchy(maxElements, summaryMode);
            out.writeInt(json.length);
            out.write(json);
            out.flush();
        } catch (IOException e) {
            // socket closed by client or timeout — not an error
        }
    }

    private static byte[] readUntilNull(DataInputStream in) throws IOException {
        byte[] buf = new byte[32];
        int pos = 0;
        while (true) {
            int b = in.readByte() & 0xFF;
            if (b == 0) {
                return Arrays.copyOf(buf, pos);
            }
            if (pos >= buf.length) {
                buf = Arrays.copyOf(buf, buf.length * 2);
            }
            buf[pos++] = (byte) b;
        }
    }

    private static boolean startsWith(byte[] data, byte[] prefix) {
        if (data.length < prefix.length) return false;
        for (int i = 0; i < prefix.length; i++) {
            if (data[i] != prefix[i]) return false;
        }
        return true;
    }

    /**
     * Parses max elements from the request.
     * "sum" or "dump"           → default for that mode
     * "sum:NNN" or "dump:NNN"   → min(NNN, ABSOLUTE_MAX_ELEMENTS), or default if NNN <= 0
     */
    private static int parseMaxElements(byte[] req, boolean summaryMode) {
        int defaultVal = summaryMode ? DEFAULT_SUM_ELEMENTS : DEFAULT_MAX_ELEMENTS;

        // Find first ':'
        int colonIdx = -1;
        for (int i = 0; i < req.length; i++) {
            if (req[i] == ':') {
                colonIdx = i;
                break;
            }
        }
        if (colonIdx < 0 || colonIdx + 1 >= req.length) {
            return defaultVal;
        }

        try {
            int n = Integer.parseInt(
                    new String(req, colonIdx + 1, req.length - colonIdx - 1,
                            StandardCharsets.US_ASCII));
            if (n <= 0) return defaultVal;
            return Math.min(n, ABSOLUTE_MAX_ELEMENTS);
        } catch (NumberFormatException e) {
            return defaultVal;
        }
    }

    /**
     * Checks if a class name ends with one of the known layout suffixes.
     * Matches against simple name (e.g. "FrameLayout", "LinearLayout"),
     * works regardless of package (android.widget, androidx, etc.).
     */
    private static boolean isLayoutClass(CharSequence className) {
        if (className == null || className.length() == 0) return false;
        String name = className.toString();
        int dot = name.lastIndexOf('.');
        String simple = dot >= 0 ? name.substring(dot + 1) : name;
        return LAYOUT_CLASS_SUFFIXES.contains(simple);
    }

    // ── dump ───────────────────────────────────────────────────────────────────

    private byte[] dumpUIHierarchy(int maxElements, boolean summaryMode) {
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
            // don't get starved by the main window exhausting maxElements.
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
                try {
                    List<AccessibilityWindowInfo> windows = ua.getWindows();
                    if (windows != null) {
                        for (int i = windows.size() - 1; i >= 0; i--) {
                            if (counter[0] >= maxElements) break;
                            AccessibilityNodeInfo root = windows.get(i).getRoot();
                            if (root != null) {
                                collectNodes(root, jw, counter, maxElements, summaryMode);
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
                    collectNodes(root, jw, counter, maxElements, summaryMode);
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
     * In summary mode, layout containers (FrameLayout, LinearLayout, etc.) are
     * skipped since they don't represent meaningful interactive elements.
     */
    private void collectNodes(AccessibilityNodeInfo node, JsonWriter jw, int[] counter,
                              int maxElements, boolean summaryMode) throws IOException {
        if (node == null || counter[0] >= maxElements) return;

        android.graphics.Rect rect = new android.graphics.Rect();
        node.getBoundsInScreen(rect);

        if (rect.width() > 0 || rect.height() > 0) {
            CharSequence text = node.getText();
            CharSequence desc = node.getContentDescription();
            String resId = node.getViewIdResourceName();
            CharSequence cls = node.getClassName();

            boolean hasText = text != null && text.length() > 0;
            boolean hasDesc = desc != null && desc.length() > 0;
            boolean hasResId = resId != null && !resId.isEmpty();
            boolean clickable = node.isClickable();

            // Only emit elements that have useful attributes
            if (hasText || hasDesc || hasResId || clickable) {
                // In summary mode, skip pure layout containers
                if (summaryMode && isLayoutClass(cls) && !clickable && !hasText && !hasDesc) {
                    // Still recurse into children — layout might contain useful widgets
                } else {
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
        }

        // Recurse into children
        int childCount = node.getChildCount();
        for (int i = 0; i < childCount; i++) {
            if (counter[0] >= maxElements) break;
            AccessibilityNodeInfo child = node.getChild(i);
            if (child != null) {
                collectNodes(child, jw, counter, maxElements, summaryMode);
            }
        }
    }

    private static byte[] buildError(String msg) {
        String s = "{\"elements\":[],\"error\":\"" + (msg != null ? msg : "unknown") + "\"}";
        return s.getBytes(StandardCharsets.UTF_8);
    }
}
