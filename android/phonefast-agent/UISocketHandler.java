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
 *             "full\0"           → hierarchical mode (all nodes, parent/depth), default max (500)
 *             "full:NNN\0"       → hierarchical mode, max N elements
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

    private static final byte[] DUMP_BYTES = "dump".getBytes(StandardCharsets.US_ASCII);
    private static final byte[] SUM_BYTES = "sum".getBytes(StandardCharsets.US_ASCII);
    private static final byte[] FULL_BYTES = "full".getBytes(StandardCharsets.US_ASCII);

    // Layout class names filtered out in summary mode (suffix matching).
    // These are non-interactive containers that just arrange children.
    private static final Set<String> LAYOUT_CLASS_SUFFIXES = new HashSet<>(Arrays.asList(
        "FrameLayout", "LinearLayout", "RelativeLayout", "ConstraintLayout",
        "AbsoluteLayout", "GridLayout", "TableLayout", "TableRow",
        "ScrollView", "HorizontalScrollView", "NestedScrollView",
        "ViewGroup", "ViewStub", "Space", "Spacer",
        "CoordinatorLayout", "DrawerLayout", "SwipeRefreshLayout",
        "Toolbar", "ToolbarLayout", "ActionBar", "ActionBarContainer",
        "BottomNavigationView", "TabLayout", "TabWidget",
        "ViewPager", "ViewPager2", "ViewAnimator", "ViewFlipper",
        "FragmentBreadCrumbs", "ContentFrameLayout"
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
                    LocalSocket client = serverSocket.accept();
                    Ln.i("phonefast: UI client connected");
                    // Keep connection alive — handle multiple requests until client disconnects.
                    while (running && handleClient(client)) {
                        // continue on same connection
                    }
                    try { client.close(); } catch (IOException ignore) {}
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

    // Returns true on success, false when the client has disconnected.
    private boolean handleClient(LocalSocket socket) {
        try {
            DataInputStream in = new DataInputStream(socket.getInputStream());
            DataOutputStream out = new DataOutputStream(socket.getOutputStream());

            // Read exactly 4 bytes. Use readFully() rather than read() because
            // read() may return fewer bytes even on a healthy local socket,
            // causing a false client-disconnect detection under load.
            byte[] prefix = new byte[4];
            in.readFully(prefix, 0, 4);

            // Determine mode: "dump" (4 bytes), "sum" (3 bytes + separator),
            // or "full" (4 bytes). For "sum", the 4th byte we read is the
            // separator (':' or '\0'), so we check the first 3 bytes.
            boolean summaryMode;
            boolean fullMode;
            if (Arrays.equals(prefix, DUMP_BYTES)) {
                summaryMode = false;
                fullMode = false;
            } else if (Arrays.equals(prefix, FULL_BYTES)) {
                summaryMode = false;
                fullMode = true;
            } else if (prefix[0] == SUM_BYTES[0] && prefix[1] == SUM_BYTES[1] && prefix[2] == SUM_BYTES[2]) {
                summaryMode = true;
                fullMode = false;
            } else {
                Ln.w("phonefast: unknown UI request");
                // The 4 bytes may contain partial data after the prefix;
                // we've already consumed them, so just return.
                return true; // protocol error but connection is still alive
            }

            // Parse limit from remaining bytes after the prefix.
            //   "dump\0"     → default (500)
            //   "dump:NN\0"  → min(NN, 500)
            //   "sum\0"      → default (100) — 4th byte was '\0'
            //   "sum:NN\0"   → min(NN, 500)
            //   "full\0"     → default (500)
            //   "full:NN\0"  → min(NN, 500)
            // The 4th byte of "sum" requests was already read into prefix[3].
            int maxElements = ABSOLUTE_MAX_ELEMENTS;
            int sep;
            if (summaryMode) {
                // For "sum" requests, prefix[3] is the separator
                sep = prefix[3] & 0xFF;
            } else {
                // For "dump" and "full" requests, read the 5th byte as separator
                sep = in.read();
            }

            if (sep == ':') {
                // Parse limit until '\0'
                int n = 0;
                while (true) {
                    int b = in.read();
                    if (b == 0) break;
                    if (b >= '0' && b <= '9') {
                        n = n * 10 + (b - '0');
                        if (n > ABSOLUTE_MAX_ELEMENTS) {
                            // Cap and drain
                            drainUntilNull(in);
                            n = ABSOLUTE_MAX_ELEMENTS;
                            break;
                        }
                    } else {
                        drainUntilNull(in);
                        n = ABSOLUTE_MAX_ELEMENTS;
                        break;
                    }
                }
                if (n > 0) maxElements = n;
            } else if (summaryMode && sep == 0) {
                // "sum\0" — the 4th byte was the null terminator
                // Use default (ABSOLUTE_MAX_ELEMENTS)
            } else if (sep != 0 && sep != -1) {
                // Unexpected byte — drain the rest
                drainUntilNull(in);
            }

            byte[] json;
            if (fullMode) {
                json = dumpFullHierarchy(maxElements);
            } else {
                json = dumpUIHierarchy(maxElements, summaryMode);
            }
            out.writeInt(json.length);
            out.write(json);
            out.flush();
            return true;
        } catch (IOException e) {
            // socket closed by client or timeout — not an error
            return false;
        }
    }

    /**
     * Reads and discards bytes from the input stream until a null terminator
     * is found. Prevents stale data from leaking between requests.
     */
    private static void drainUntilNull(DataInputStream in) throws IOException {
        while (true) {
            int b = in.read();
            if (b == 0 || b == -1) break;
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

    /**
     * Shortens common Android widget class names for summary mode.
     * e.g. "android.widget.TextView" → "Text", "android.widget.ImageView" → "Image".
     * Handles both fully-qualified and simple (already-stripped) names.
     */
    private static String simplifyClassName(String fullName) {
        if (fullName == null || fullName.isEmpty()) return fullName;
        int dot = fullName.lastIndexOf('.');
        String simple = dot >= 0 ? fullName.substring(dot + 1) : fullName;
        switch (simple) {
            case "TextView":
            case "CheckedTextView":
            case "AppCompatTextView":
            case "MaterialTextView":
                return "Text";
            case "ImageView":
            case "AppCompatImageView":
            case "MaterialImageView":
                return "Image";
            case "Button":
            case "AppCompatButton":
            case "MaterialButton":
                return "Button";
            case "ImageButton":
                return "IconBtn";
            case "EditText":
            case "AppCompatEditText":
            case "MaterialEditText":
                return "Input";
            case "CheckBox":
            case "AppCompatCheckBox":
            case "MaterialCheckBox":
                return "Check";
            case "RadioButton":
            case "AppCompatRadioButton":
            case "MaterialRadioButton":
                return "Radio";
            case "Switch":
            case "SwitchCompat":
            case "MaterialSwitch":
                return "Switch";
            case "ProgressBar":
            case "AppCompatProgressBar":
            case "MaterialProgressBar":
                return "Progress";
            case "SeekBar":
            case "AppCompatSeekBar":
            case "MaterialSeekBar":
                return "Seek";
            case "RatingBar":
                return "Rating";
            case "Spinner":
                return "Select";
            case "ToggleButton":
                return "Toggle";
            case "WebView":
            case "WebViewClassic":
                return "Browser";
            default:
                return simple;
        }
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
                    jw.name("class_name").value(
                        (cls != null && summaryMode) ?
                            simplifyClassName(cls.toString()) :
                            (cls != null ? cls.toString() : "")
                    );

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

    // ── full hierarchical dump (all nodes, no filtering) ──────────────────────

    private byte[] dumpFullHierarchy(int maxElements) {
        UiAutomation ua = uiAutomation;
        if (ua == null) {
            return buildError("UiAutomation not available");
        }

        StringWriter sw = new StringWriter(16384);
        try {
            JsonWriter jw = new JsonWriter(sw);
            jw.beginObject();
            jw.name("elements");
            jw.beginArray();

            int[] counter = {0};
            // Iterate windows in reverse order (topmost first)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
                try {
                    List<AccessibilityWindowInfo> windows = ua.getWindows();
                    if (windows != null) {
                        for (int i = windows.size() - 1; i >= 0; i--) {
                            if (counter[0] >= maxElements) break;
                            AccessibilityNodeInfo root = windows.get(i).getRoot();
                            if (root != null) {
                                collectFullNodes(root, jw, counter, maxElements, -1, 0);
                            }
                        }
                    }
                } catch (Exception e) {
                    Ln.w("phonefast: getWindows failed, falling back: " + e.getMessage());
                }
            }

            if (counter[0] == 0) {
                AccessibilityNodeInfo root = ua.getRootInActiveWindow();
                if (root != null) {
                    collectFullNodes(root, jw, counter, maxElements, -1, 0);
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
     * Recursively collect ALL nodes (no filtering) with parent/depth metadata.
     * This is used to generate hierarchical formats (jsonl, simplexml, flatref)
     * where the full tree structure is needed.
     */
    private void collectFullNodes(AccessibilityNodeInfo node, JsonWriter jw, int[] counter,
                                   int maxElements, int parentId, int depth) throws IOException {
        if (node == null || counter[0] >= maxElements) return;

        android.graphics.Rect rect = new android.graphics.Rect();
        node.getBoundsInScreen(rect);

        if (rect.width() > 0 || rect.height() > 0) {
            int nodeId = counter[0]++;

            CharSequence text = node.getText();
            CharSequence desc = node.getContentDescription();
            String resId = node.getViewIdResourceName();
            CharSequence cls = node.getClassName();

            jw.beginObject();
            jw.name("id").value(nodeId);
            jw.name("parent").value(parentId);
            jw.name("depth").value(depth);
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

            jw.name("clickable").value(node.isClickable());
            jw.name("enabled").value(node.isEnabled());
            jw.name("focused").value(node.isFocused());
            jw.name("selected").value(node.isSelected());
            jw.endObject();

            // Recurse into children
            int childCount = node.getChildCount();
            for (int i = 0; i < childCount; i++) {
                if (counter[0] >= maxElements) break;
                AccessibilityNodeInfo child = node.getChild(i);
                if (child != null) {
                    collectFullNodes(child, jw, counter, maxElements, nodeId, depth + 1);
                }
            }
        }
    }

    private static byte[] buildError(String msg) {
        String s = "{\"elements\":[],\"error\":\"" + (msg != null ? msg : "unknown") + "\"}";
        return s.getBytes(StandardCharsets.UTF_8);
    }
}
