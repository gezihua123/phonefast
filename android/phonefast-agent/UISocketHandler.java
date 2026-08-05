package com.genymobile.scrcpy.control;

import android.app.UiAutomation;
import android.content.res.Resources;
import android.os.Build;
import android.os.Looper;
import android.util.DisplayMetrics;
import android.util.JsonWriter;
import android.view.accessibility.AccessibilityNodeInfo;
import android.view.accessibility.AccessibilityWindowInfo;

import com.genymobile.scrcpy.util.Ln;

import java.io.ByteArrayOutputStream;
import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.io.IOException;
import java.io.OutputStreamWriter;
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
 *   Request:  "sum\0"            → summary mode (default, shrink optimized)
 *             "sum:NNN\0"        → summary mode, max N elements
 *             "full\0"           → full hierarchical mode (all nodes, parent/depth)
 *             "full:NNN\0"       → full mode, max N elements
 *             "dump\0"           → backward compat (treated as "sum")
 *   Response: 4-byte big-endian length + JSON bytes
 *
 * Summary mode applies shrink optimizations (skip inactive windows, maxDepth,
 * skip pure images, skip layout containers) for token-efficient output.
 * Full mode collects all visible nodes with hierarchy metadata.
 *
 * UiAutomation is initialised inside the phonefast-ui thread via reflection
 * (same mechanism used by "uiautomator dump"), so no Instrumentation is needed.
 */
public final class UISocketHandler {

    private static final String UI_SOCKET_SUFFIX = "_ui";
    // Absolute hard cap — never collect more than this per dump (avoids OOM)
    private static final int ABSOLUTE_MAX_ELEMENTS = 2000;

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
    private volatile LocalServerSocket serverSocket; // closed by stop() to unblock accept()
    // Reused across dumps to avoid per-call StringWriter/String/byte[] allocation
    // (full-mode JSON ~50KB → ~150KB transient garbage per dump → GC pressure → Binder timeouts).
    private final ByteArrayOutputStream jsonBuf = new ByteArrayOutputStream(65536);
    // Reused Rect to avoid per-node allocation (284 nodes/dump = 284 Rect objects)
    private final android.graphics.Rect rectBuf = new android.graphics.Rect();
    // Screen dimensions for visibility calculation (refreshed per dump to handle rotation)
    private int screenWidth = 1080;
    private int screenHeight = 1920;

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
                serverSocket = new LocalServerSocket(socketName);
                Ln.i("phonefast: UI socket ready on " + socketName);

                while (running) {
                    LocalSocket client = serverSocket.accept();
                    Ln.i("phonefast: UI client connected");
                    try {
                        // Keep connection alive — handle multiple requests until client disconnects.
                        while (running && handleClient(client)) {
                            // continue on same connection
                        }
                    } catch (Exception e) {
                        // RuntimeException from dumpUIHierarchy (stale node,
                        // SecurityException, OOM, etc.) — must NOT kill the
                        // accept thread. Log and close the connection so the
                        // client can reconnect on a fresh socket.
                        Ln.e("phonefast: UI handler crashed: " + e);
                    } finally {
                        // Always close the client socket, even if handleClient
                        // threw an unchecked exception. Without this, the
                        // socket leaks and the Go client hangs on a dead conn.
                        try { client.close(); } catch (IOException ignore) {}
                    }
                }
                LocalServerSocket ss = serverSocket;
                if (ss != null) ss.close();
            } catch (IOException e) {
                Ln.e("phonefast: UI socket server error: " + e.getMessage());
            }
        }, "phonefast-ui").start();
    }


    public void stop() {
        running = false;
        // Close server socket to unblock accept() — without this the phonefast-ui
        // thread hangs on accept() and prevents clean daemon shutdown/restart.
        LocalServerSocket ss = serverSocket;
        if (ss != null) {
            try { ss.close(); } catch (IOException ignore) { }
            serverSocket = null;
        }
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

            // Determine mode: "sum" (summary, 3 bytes) or "full" (4 bytes).
            // "dump" is treated as "sum" for backward compatibility.
            // For "sum", the 4th byte is the separator (':' or '\0').
            boolean summaryMode;
            boolean fullMode;
            if (Arrays.equals(prefix, FULL_BYTES)) {
                // "full" (4 bytes) — full hierarchical mode
                summaryMode = false;
                fullMode = true;
            } else if (prefix[0] == SUM_BYTES[0] && prefix[1] == SUM_BYTES[1] && prefix[2] == SUM_BYTES[2]) {
                // "sum" (3 bytes + separator) or "dump" (backward compat, treated as summary)
                summaryMode = true;
                fullMode = false;
            } else {
                Ln.w("phonefast: unknown UI request");
                // The 4 bytes may contain partial data after the prefix;
                // we've already consumed them, so just return.
                return true; // protocol error but connection is still alive
            }

            // Parse limit from remaining bytes after the prefix.
            //   "sum\0"      → default (2000)
            //   "sum:NN\0"   → min(NN, 2000)
            //   "full\0"     → default (2000)
            //   "full:NN\0"  → min(NN, 2000)
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

            if (fullMode) {
                dumpFullHierarchy(maxElements, out);
            } else {
                dumpUIHierarchy(maxElements, summaryMode, out);
            }
            out.flush();
            return true;
        } catch (IOException e) {
            // socket closed by client or timeout — not an error
            return false;
        } catch (Exception e) {
            // RuntimeException (stale node, SecurityException, OOM, etc.) —
            // log it but keep the accept thread alive. Return false to close
            // this connection so the client gets a fresh socket next time.
            Ln.e("phonefast: UI dump failed: " + e);
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

    /**
     * Refresh screen dimensions from system display metrics.
     * Called per-dump to handle device rotation correctly.
     * Falls back to active window bounds if DisplayMetrics is unavailable.
     * @param ua the UiAutomation already null-checked by the caller (avoids
     *           re-reading the volatile field — TOCTOU during shutdown).
     */
    private void ensureScreenSize(UiAutomation ua) {
        try {
            DisplayMetrics metrics = Resources.getSystem().getDisplayMetrics();
            screenWidth = metrics.widthPixels;
            screenHeight = metrics.heightPixels;
            return;
        } catch (Exception e) {
            Ln.w("phonefast: DisplayMetrics failed, trying window bounds: " + e.getMessage());
        }
        // Fallback: use active window bounds from the caller-provided UiAutomation
        try {
            AccessibilityNodeInfo root = ua.getRootInActiveWindow();
            if (root != null) {
                try {
                    root.getBoundsInScreen(rectBuf);
                    screenWidth = rectBuf.right;
                    screenHeight = rectBuf.bottom;
                    return;
                } finally {
                    root.recycle();
                }
            }
        } catch (Exception e2) {
            Ln.w("phonefast: window bounds fallback also failed: " + e2.getMessage());
        }
    }

    /**
     * Returns true if the rect (in screen coordinates) has any overlap with
     * the visible screen area [0, 0, screenWidth, screenHeight].
     * Elements entirely above/below/left/right of the screen are invisible.
     */
    private boolean isOnScreen(int left, int top, int right, int bottom) {
        return left < screenWidth && right > 0 && top < screenHeight && bottom > 0;
    }

    // ── dump ───────────────────────────────────────────────────────────────────

    private void dumpUIHierarchy(int maxElements, boolean summaryMode, DataOutputStream out) throws IOException {
        UiAutomation ua = uiAutomation;
        if (ua == null) {
            buildError("UiAutomation not available", out);
            return;
        }

        ensureScreenSize(ua);

        jsonBuf.reset();
        try {
            long t0 = System.currentTimeMillis();

            JsonWriter jw = new JsonWriter(new OutputStreamWriter(jsonBuf, StandardCharsets.UTF_8));
            jw.beginObject();
            jw.name("elements");
            jw.beginArray();

            int[] counter = {0};
            // Try all windows first (gives more complete picture)
            // Iterate in REVERSE order: topmost windows (dialogs, sheets)
            // come last in z-order but should be processed FIRST so they
            // don't get starved by the main window exhausting maxElements.
            //
            // RECYCLE NOTE: Recycle in leaf→root order. Each window and its
            // root node are recycled inside the same try-finally block after
            // the entire tree has been collected. DO NOT extract window.recycle()
            // into a separate loop — that would recycle windows while their
            // root nodes are still in use (over-recycling), causing stale data.
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
                List<AccessibilityWindowInfo> windows = null;
                int lastVisited = -1; // tracks the last index whose window was recycled
                try {
                    windows = ua.getWindows();
                    if (windows != null) {
                        for (int i = windows.size() - 1; i >= 0; i--) {
                            if (counter[0] >= maxElements) break;
                            AccessibilityWindowInfo window = windows.get(i);
                            // 跳过非活动 application 窗口（当有多个窗口时）
                            if (summaryMode && windows.size() > 1 && window.getType() == AccessibilityWindowInfo.TYPE_APPLICATION && !window.isActive()) {
                                window.recycle();
                                continue;
                            }
                            AccessibilityNodeInfo root = null;
                            try {
                                root = window.getRoot();
                                if (root != null) {
                                    collectNodes(root, jw, counter, maxElements, summaryMode, 0);
                                }
                            } finally {
                                // Recycle root BEFORE window: the Android API
                                // contract says nodes sourced from a window
                                // may become invalid after window.recycle().
                                if (root != null) root.recycle();
                                window.recycle();
                                lastVisited = i;
                            }
                        }
                    }
                } catch (Exception e) {
                    Ln.w("phonefast: getWindows failed, falling back: " + e.getMessage());
                    // fall through to getRootInActiveWindow
                } finally {
                    // Recycle any windows that were NOT visited (early break on
                    // maxElements or exception). Visited windows already recycled
                    // in the inner finally — we only touch indices < lastVisited
                    // (in reverse iteration, unvisited are at lower indices).
                    if (windows != null) {
                        for (int i = lastVisited - 1; i >= 0; i--) {
                            AccessibilityWindowInfo w = windows.get(i);
                            if (w != null) w.recycle();
                        }
                    }
                }
            }

            // Fallback or supplement: active window root
            if (counter[0] == 0) {
                AccessibilityNodeInfo root = ua.getRootInActiveWindow();
                if (root != null) {
                    try {
                        collectNodes(root, jw, counter, maxElements, summaryMode, 0);
                    } finally {
                        root.recycle();
                    }
                }
            }

            jw.endArray();
            jw.endObject();
            jw.close();

            long t1 = System.currentTimeMillis();
            out.writeInt(jsonBuf.size());
            jsonBuf.writeTo(out);

            long t2 = System.currentTimeMillis();
            logDumpTiming("summary", t0, t1, t2, counter[0]);

        } catch (Exception e) {
            buildError(e.getMessage(), out);
        }
    }

    /**
     * Recursively collect nodes into a flat JSON array.
     * In summary mode, layout containers (FrameLayout, LinearLayout, etc.) are
     * skipped since they don't represent meaningful interactive elements.
     */
    private void collectNodes(AccessibilityNodeInfo node, JsonWriter jw, int[] counter,
                              int maxElements, boolean summaryMode, int depth) throws IOException {
        if (node == null || counter[0] >= maxElements) return;
        if (summaryMode && depth >= 20) return;

        node.getBoundsInScreen(rectBuf);

        if (rectBuf.width() > 0 || rectBuf.height() > 0) {

                // Read node properties (Binder cached, no extra IPC)
                CharSequence text = node.getText();
                CharSequence desc = node.getContentDescription();
                String resId = node.getViewIdResourceName();
                CharSequence cls = node.getClassName();

                // Cache toString() to avoid repeated calls (GC optimization)
                String textStr = text != null ? text.toString() : "";
                if (textStr.length() > 80) textStr = textStr.substring(0, 77) + "...";
                String descStr = desc != null ? desc.toString() : "";
                if (descStr.length() > 80) descStr = descStr.substring(0, 77) + "...";
                String clsStr = cls != null ? cls.toString() : "";

                boolean hasText = textStr.length() > 0;
                boolean hasDesc = descStr.length() > 0;
                boolean hasResId = resId != null && !resId.isEmpty();
                boolean clickable = node.isClickable();

                // Only emit elements that have useful attributes
            // Skip pure images (ImageView without text/desc/clickable) — reduces JSON output
            boolean isImageOnly = summaryMode && clsStr.endsWith("ImageView")
                && !hasText && !hasDesc && !clickable;
            if (isImageOnly) {
                // Don't write JSON, but continue recursing into children
            } else if (hasText || hasDesc || clickable) {
                    // In summary mode, skip pure layout containers
                    if (summaryMode && isLayoutClass(clsStr) && !clickable && !hasText && !hasDesc) {
                        // Still recurse into children — layout might contain useful widgets
                    } else {

                        jw.beginObject();
                        jw.name("index").value(counter[0]++);
                        jw.name("text").value(textStr);
                        jw.name("content_desc").value(descStr);
                        jw.name("resource_id").value(resId != null ? resId : "");
                        jw.name("class_name").value(simplifyClassName(clsStr));

                        jw.name("bounds");
                        jw.beginArray();
                        jw.value(rectBuf.left); jw.value(rectBuf.top);
                        jw.value(rectBuf.right); jw.value(rectBuf.bottom);
                        jw.endArray();

                        jw.name("center");
                        jw.beginArray();
                        jw.value((rectBuf.left + rectBuf.right) / 2);
                        jw.value((rectBuf.top + rectBuf.bottom) / 2);
                        jw.endArray();

                        jw.name("clickable").value(clickable);
                        jw.name("enabled").value(node.isEnabled());
                        if (!isOnScreen(rectBuf.left, rectBuf.top, rectBuf.right, rectBuf.bottom)) {
                            jw.name("visible").value(false);
                        }
                        jw.endObject();
                    }
                }

            // Recurse into children — only for visible nodes (bounds > 0).
            // GONE/unlaid-out nodes have bounds=0 and their children are also
            // invisible, so skipping them avoids unnecessary Binder calls
            // (getChildCount + getChild), reducing P50 from ~55ms to ~35ms.
            // Children are recycled BEFORE the parent (leaf→root order),
            // matching the Android API contract: parent.recycle() may
            // invalidate child objects still in use.
            int childCount = node.getChildCount();
            for (int i = 0; i < childCount; i++) {
                if (counter[0] >= maxElements) break;
                AccessibilityNodeInfo child = node.getChild(i);
                if (child != null) {
                    try {
                        collectNodes(child, jw, counter, maxElements, summaryMode, depth + 1);
                    } finally {
                        child.recycle();
                    }
                }
            }
        }
    }

    // ── full hierarchical dump (all nodes, no filtering) ──────────────────────

    private void dumpFullHierarchy(int maxElements, DataOutputStream out) throws IOException {
        UiAutomation ua = uiAutomation;
        if (ua == null) {
            buildError("UiAutomation not available", out);
            return;
        }

        ensureScreenSize(ua);

        jsonBuf.reset();
        try {
            long t0 = System.currentTimeMillis();

            JsonWriter jw = new JsonWriter(new OutputStreamWriter(jsonBuf, StandardCharsets.UTF_8));
            jw.beginObject();
            jw.name("elements");
            jw.beginArray();

            int[] counter = {0};
            // Iterate windows in reverse order (topmost first).
            // Recycle window+root together after the tree is fully collected;
            // never recycle windows in a separate loop (over-recycling).
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
                List<AccessibilityWindowInfo> windows = null;
                int lastVisited = -1;
                try {
                    windows = ua.getWindows();
                    if (windows != null) {
                        for (int i = windows.size() - 1; i >= 0; i--) {
                            if (counter[0] >= maxElements) break;
                            AccessibilityWindowInfo window = windows.get(i);
                            AccessibilityNodeInfo root = null;
                            try {
                                root = window.getRoot();
                                if (root != null) {
                                    collectFullNodes(root, jw, counter, maxElements, -1, 0);
                                }
                            } finally {
                                if (root != null) root.recycle();
                                window.recycle();
                                lastVisited = i;
                            }
                        }
                    }
                } catch (Exception e) {
                    Ln.w("phonefast: getWindows failed, falling back: " + e.getMessage());
                } finally {
                    if (windows != null) {
                        for (int i = lastVisited - 1; i >= 0; i--) {
                            AccessibilityWindowInfo w = windows.get(i);
                            if (w != null) w.recycle();
                        }
                    }
                }
            }

            if (counter[0] == 0) {
                AccessibilityNodeInfo root = ua.getRootInActiveWindow();
                if (root != null) {
                    try {
                        collectFullNodes(root, jw, counter, maxElements, -1, 0);
                    } finally {
                        root.recycle();
                    }
                }
            }

            jw.endArray();
            jw.endObject();
            jw.close();

            long t1 = System.currentTimeMillis();
            out.writeInt(jsonBuf.size());
            jsonBuf.writeTo(out);

            long t2 = System.currentTimeMillis();
            logDumpTiming("full", t0, t1, t2, counter[0]);

        } catch (Exception e) {
            buildError(e.getMessage(), out);
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

        node.getBoundsInScreen(rectBuf);

        if (rectBuf.width() > 0 || rectBuf.height() > 0) {
            int nodeId = counter[0]++;

            CharSequence text = node.getText();
            CharSequence desc = node.getContentDescription();
            String resId = node.getViewIdResourceName();
            CharSequence cls = node.getClassName();

            jw.beginObject();
            jw.name("id").value(nodeId);
            jw.name("parent").value(parentId);
            jw.name("depth").value(depth);
            // Text preview: truncate long text (all modes)
            String textStr = text != null ? text.toString() : "";
            if (textStr.length() > 80) textStr = textStr.substring(0, 77) + "...";
            String descStr = desc != null ? desc.toString() : "";
            if (descStr.length() > 80) descStr = descStr.substring(0, 77) + "...";
            jw.name("text").value(textStr);
            jw.name("content_desc").value(descStr);
            jw.name("resource_id").value(resId != null ? resId : "");
            jw.name("class_name").value(cls != null ? simplifyClassName(cls.toString()) : "");

            jw.name("bounds");
            jw.beginArray();
            jw.value(rectBuf.left); jw.value(rectBuf.top);
            jw.value(rectBuf.right); jw.value(rectBuf.bottom);
            jw.endArray();

            jw.name("center");
            jw.beginArray();
            jw.value((rectBuf.left + rectBuf.right) / 2);
            jw.value((rectBuf.top + rectBuf.bottom) / 2);
            jw.endArray();

            jw.name("clickable").value(node.isClickable());
            jw.name("enabled").value(node.isEnabled());
            jw.name("focused").value(node.isFocused());
            jw.name("selected").value(node.isSelected());
            if (!isOnScreen(rectBuf.left, rectBuf.top, rectBuf.right, rectBuf.bottom)) {
                jw.name("visible").value(false);
            }
            jw.endObject();

            // Recurse into children — recycle each child after its subtree is processed.
            int childCount = node.getChildCount();
            for (int i = 0; i < childCount; i++) {
                if (counter[0] >= maxElements) break;
                AccessibilityNodeInfo child = node.getChild(i);
                if (child != null) {
                    try {
                        collectFullNodes(child, jw, counter, maxElements, nodeId, depth + 1);
                    } finally {
                        child.recycle();
                    }
                }
            }
        }
    }

    /**
     * Logs a dump timing breakdown when total exceeds 80ms, so slow dumps
     * are diagnosable without flooding the log on fast (normal) dumps.
     * t0=dump start, t1=jw.close() done (collect end), t2=socket write done.
     */
    private void logDumpTiming(String mode, long t0, long t1, long t2, int nodeCount) {
        if (t2 - t0 <= 80) return;
        Ln.i("phonefast: dump(" + mode + ") total=" + (t2 - t0) + "ms collect=" + (t1 - t0)
            + "ms send=" + (t2 - t1) + "ms nodes=" + nodeCount
            + " json_kb=" + (jsonBuf.size() / 1024));
    }

    /**
     * Writes a length-prefixed error JSON payload straight onto the socket
     * output stream, reusing {@link #jsonBuf}. The message is routed through
     * JsonWriter so quotes/backslashes/control chars in exception messages
     * (e.g. {@code Cannot invoke "foo()"}) are properly escaped — otherwise
     * the JSON is malformed and the Go side fails to unmarshal, masking the
     * real server error as a transport error.
     */
    private void buildError(String msg, DataOutputStream out) throws IOException {
        jsonBuf.reset();
        JsonWriter jw = new JsonWriter(new OutputStreamWriter(jsonBuf, StandardCharsets.UTF_8));
        jw.beginObject();
        jw.name("elements").beginArray().endArray();
        jw.name("error").value(msg != null ? msg : "unknown");
        jw.endObject();
        jw.close();
        out.writeInt(jsonBuf.size());
        jsonBuf.writeTo(out);
    }
}
