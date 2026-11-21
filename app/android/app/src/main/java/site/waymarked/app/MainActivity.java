package site.waymarked.app;

import android.Manifest;
import android.annotation.SuppressLint;
import android.app.Activity;
import android.content.ActivityNotFoundException;
import android.content.ContentValues;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.provider.MediaStore;
import android.util.Base64;
import android.view.WindowInsets;
import android.view.WindowManager;
import android.webkit.JavascriptInterface;
import android.webkit.PermissionRequest;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;
import android.widget.Toast;

import java.io.ByteArrayInputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.util.HashMap;
import java.util.Map;

/**
 * Waymark for Android: a shell around the browser Controller page, bundled
 * in the APK and served from a private https origin so camera, WebRTC and
 * Web Crypto work. The page talks only to the Agent on the local network;
 * the WebView loads nothing from the internet.
 */
public class MainActivity extends Activity {
    private static final String HOST = "appassets.androidplatform.net";
    private static final String BASE = "https://" + HOST + "/remote/index.html";
    private static final int REQ_CAMERA = 1;
    private static final int REQ_FILES = 2;

    private WebView web;
    private PermissionRequest pendingPermission;
    private ValueCallback<Uri[]> pendingFiles;

    @SuppressLint("SetJavaScriptEnabled")
    @Override
    protected void onCreate(Bundle state) {
        super.onCreate(state);
        web = new WebView(this);
        web.setBackgroundColor(getColor(R.color.bg));
        // Android 15 draws apps edge to edge: keep the page clear of the
        // status bar, the navigation bar and a camera cutout. WebView draws
        // its page over its own padding, so the padding goes on a frame
        // around it.
        FrameLayout root = new FrameLayout(this);
        root.setBackgroundColor(getColor(R.color.bg));
        root.addView(web, new FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));
        root.setOnApplyWindowInsetsListener((v, in) -> {
            int l, t, r, b;
            if (Build.VERSION.SDK_INT >= 30) {
                android.graphics.Insets i = in.getInsets(WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout() | WindowInsets.Type.ime());
                l = i.left; t = i.top; r = i.right; b = i.bottom;
            } else {
                l = in.getSystemWindowInsetLeft(); t = in.getSystemWindowInsetTop();
                r = in.getSystemWindowInsetRight(); b = in.getSystemWindowInsetBottom();
            }
            v.setPadding(l, t, r, b);
            return Build.VERSION.SDK_INT >= 30 ? WindowInsets.CONSUMED : in.consumeSystemWindowInsets();
        });
        setContentView(root);
        root.requestApplyInsets();
        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setMediaPlaybackRequiresUserGesture(false);
        s.setAllowFileAccess(false);
        s.setAllowContentAccess(false);
        s.setGeolocationEnabled(false);
        s.setSupportMultipleWindows(false);
        web.setWebViewClient(new Client());
        web.setWebChromeClient(new Chrome());
        web.addJavascriptInterface(new Bridge(), "WaymarkAndroid");
        web.loadUrl(BASE + hashFrom(getIntent()));
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        String hash = hashFrom(intent);
        if (!hash.isEmpty()) web.loadUrl(BASE + hash);
    }

    /** Finds the QR payload in a waymarked.site link or in shared text. */
    private static String hashFrom(Intent intent) {
        if (intent == null) return "";
        String text = null;
        if (Intent.ACTION_VIEW.equals(intent.getAction()) && intent.getData() != null) {
            text = intent.getData().toString();
        } else if (Intent.ACTION_SEND.equals(intent.getAction())) {
            text = intent.getStringExtra(Intent.EXTRA_TEXT);
        }
        if (text == null) return "";
        int i = text.indexOf("WM1.");
        if (i < 0) return "";
        String p = text.substring(i).split("[\\s#&?]")[0];
        return p.matches("WM1\\.[0-9A-Za-z_.,-]+") ? "#" + p : "";
    }

    @Override
    public void onBackPressed() {
        if (web.canGoBack()) web.goBack();
        else super.onBackPressed();
    }

    @Override
    protected void onDestroy() {
        web.destroy();
        super.onDestroy();
    }

    private class Client extends WebViewClient {
        @Override
        public WebResourceResponse shouldInterceptRequest(WebView view, WebResourceRequest req) {
            Uri u = req.getUrl();
            if (!"https".equals(u.getScheme()) || !HOST.equals(u.getHost())) {
                // Nothing leaves the phone through the WebView.
                return new WebResourceResponse("text/plain", "utf-8", 403, "Forbidden", null, new ByteArrayInputStream(new byte[0]));
            }
            String path = u.getPath() == null ? "" : u.getPath();
            if (path.contains("..") || !path.startsWith("/remote/")) return notFound();
            try {
                InputStream in = getAssets().open(path.substring(1));
                WebResourceResponse r = new WebResourceResponse(mime(path), null, in);
                Map<String, String> h = new HashMap<>();
                h.put("Cache-Control", "no-store");
                r.setResponseHeaders(h);
                return r;
            } catch (IOException e) {
                return notFound();
            }
        }

        @Override
        public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest req) {
            Uri u = req.getUrl();
            if (HOST.equals(u.getHost())) return false;
            // Any other link opens outside the app.
            try {
                startActivity(new Intent(Intent.ACTION_VIEW, u));
            } catch (ActivityNotFoundException ignored) {
            }
            return true;
        }
    }

    private static WebResourceResponse notFound() {
        return new WebResourceResponse("text/plain", "utf-8", 404, "Not Found", null, new ByteArrayInputStream(new byte[0]));
    }

    private static String mime(String path) {
        if (path.endsWith(".html")) return "text/html";
        if (path.endsWith(".js") || path.endsWith(".mjs")) return "text/javascript";
        if (path.endsWith(".css")) return "text/css";
        if (path.endsWith(".svg")) return "image/svg+xml";
        if (path.endsWith(".png")) return "image/png";
        if (path.endsWith(".json")) return "application/json";
        if (path.endsWith(".txt")) return "text/plain";
        return "application/octet-stream";
    }

    private class Chrome extends WebChromeClient {
        @Override
        public void onPermissionRequest(PermissionRequest request) {
            runOnUiThread(() -> {
                boolean video = false;
                for (String r : request.getResources()) {
                    if (PermissionRequest.RESOURCE_VIDEO_CAPTURE.equals(r)) video = true;
                }
                if (!video) {
                    request.deny();
                    return;
                }
                if (checkSelfPermission(Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) {
                    request.grant(new String[]{PermissionRequest.RESOURCE_VIDEO_CAPTURE});
                } else {
                    if (pendingPermission != null) pendingPermission.deny();
                    pendingPermission = request;
                    requestPermissions(new String[]{Manifest.permission.CAMERA}, REQ_CAMERA);
                }
            });
        }

        @Override
        public boolean onShowFileChooser(WebView view, ValueCallback<Uri[]> callback, FileChooserParams params) {
            if (pendingFiles != null) pendingFiles.onReceiveValue(null);
            pendingFiles = callback;
            Intent i = params.createIntent();
            if (params.getMode() == FileChooserParams.MODE_OPEN_MULTIPLE) {
                i.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true);
            }
            try {
                startActivityForResult(i, REQ_FILES);
            } catch (ActivityNotFoundException e) {
                pendingFiles = null;
                return false;
            }
            return true;
        }
    }

    @Override
    public void onRequestPermissionsResult(int code, String[] perms, int[] results) {
        if (code != REQ_CAMERA || pendingPermission == null) return;
        if (results.length > 0 && results[0] == PackageManager.PERMISSION_GRANTED) {
            pendingPermission.grant(new String[]{PermissionRequest.RESOURCE_VIDEO_CAPTURE});
        } else {
            pendingPermission.deny();
        }
        pendingPermission = null;
    }

    @Override
    protected void onActivityResult(int code, int result, Intent data) {
        if (code != REQ_FILES || pendingFiles == null) {
            super.onActivityResult(code, result, data);
            return;
        }
        Uri[] uris = null;
        if (result == RESULT_OK && data != null) {
            if (data.getClipData() != null) {
                int n = data.getClipData().getItemCount();
                uris = new Uri[n];
                for (int i = 0; i < n; i++) uris[i] = data.getClipData().getItemAt(i).getUri();
            } else if (data.getData() != null) {
                uris = new Uri[]{data.getData()};
            }
        }
        pendingFiles.onReceiveValue(uris);
        pendingFiles = null;
    }

    /**
     * Saves downloads into Downloads (a WebView cannot save blob: links) and
     * keeps the screen on while connected. Only the bundled page can call it,
     * because the WebView loads no other origin.
     */
    private class Bridge {
        private final Map<Integer, OutputStream> open = new HashMap<>();
        private final Map<Integer, String> names = new HashMap<>();
        private int next = 1;

        @JavascriptInterface
        public synchronized int begin(String name) {
            String safe = name.replaceAll("[\\\\/:*?\"<>|\\x00-\\x1f]", "_");
            if (safe.isEmpty() || safe.startsWith(".")) safe = "file" + safe;
            try {
                OutputStream out;
                if (Build.VERSION.SDK_INT >= 29) {
                    ContentValues v = new ContentValues();
                    v.put(MediaStore.Downloads.DISPLAY_NAME, safe);
                    v.put(MediaStore.Downloads.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS + "/Waymark");
                    Uri uri = getContentResolver().insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, v);
                    if (uri == null) return 0;
                    out = getContentResolver().openOutputStream(uri);
                } else {
                    if (checkSelfPermission(Manifest.permission.WRITE_EXTERNAL_STORAGE) != PackageManager.PERMISSION_GRANTED) {
                        requestPermissions(new String[]{Manifest.permission.WRITE_EXTERNAL_STORAGE}, 3);
                        return 0;
                    }
                    File dir = new File(Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS), "Waymark");
                    dir.mkdirs();
                    File f = new File(dir, safe);
                    for (int i = 2; f.exists(); i++) f = new File(dir, i + " " + safe);
                    out = new FileOutputStream(f);
                }
                if (out == null) return 0;
                int id = next++;
                open.put(id, out);
                names.put(id, safe);
                return id;
            } catch (IOException | SecurityException e) {
                return 0;
            }
        }

        @JavascriptInterface
        public synchronized boolean append(int id, String b64) {
            OutputStream out = open.get(id);
            if (out == null) return false;
            try {
                out.write(Base64.decode(b64, Base64.DEFAULT));
                return true;
            } catch (IOException | IllegalArgumentException e) {
                return false;
            }
        }

        @JavascriptInterface
        public synchronized boolean finish(int id) {
            OutputStream out = open.remove(id);
            String name = names.remove(id);
            if (out == null) return false;
            boolean ok;
            try {
                out.close();
                ok = true;
            } catch (IOException e) {
                ok = false;
            }
            final String msg = ok ? getString(R.string.saved, name) : getString(R.string.save_failed);
            runOnUiThread(() -> Toast.makeText(MainActivity.this, msg, Toast.LENGTH_LONG).show());
            return ok;
        }

        @JavascriptInterface
        public void keepAwake(boolean on) {
            runOnUiThread(() -> {
                if (on) getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
                else getWindow().clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
            });
        }
    }
}
