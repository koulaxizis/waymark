package site.waymarked.app;

import android.annotation.SuppressLint;
import android.annotation.TargetApi;
import android.bluetooth.BluetoothAdapter;
import android.bluetooth.BluetoothClass;
import android.bluetooth.BluetoothDevice;
import android.bluetooth.BluetoothHidDevice;
import android.bluetooth.BluetoothHidDeviceAppSdpSettings;
import android.bluetooth.BluetoothManager;
import android.bluetooth.BluetoothProfile;
import android.content.Context;
import android.os.Build;

import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.Executor;

/**
 * The phone as a Bluetooth keyboard that changes slides (Proposal 14, point
 * 3). Any presentation program on the paired computer takes Page Down and
 * Page Up, so no Agent is needed. Android 9 and later; the phone maker must
 * not have removed the HID device profile.
 *
 * Callers check the Bluetooth permissions first (Android 12+), hence the
 * MissingPermission suppression. Every method runs on the main thread.
 */
@TargetApi(Build.VERSION_CODES.P)
@SuppressLint("MissingPermission")
final class Clicker {
    interface Listener {
        void onState(String json);
    }

    // A boot keyboard with report id 1: one modifier byte, one reserved
    // byte, six key codes.
    private static final byte[] DESCRIPTOR = {
            0x05, 0x01, 0x09, 0x06, (byte) 0xA1, 0x01, (byte) 0x85, 0x01,
            0x05, 0x07, 0x19, (byte) 0xE0, 0x29, (byte) 0xE7, 0x15, 0x00, 0x25, 0x01,
            0x75, 0x01, (byte) 0x95, 0x08, (byte) 0x81, 0x02,
            (byte) 0x95, 0x01, 0x75, 0x08, (byte) 0x81, 0x01,
            (byte) 0x95, 0x06, 0x75, 0x08, 0x15, 0x00, 0x25, 0x65,
            0x05, 0x07, 0x19, 0x00, 0x29, 0x65, (byte) 0x81, 0x00,
            (byte) 0xC0,
    };
    private static final int REPORT_ID = 1;
    private static final int SHIFT = 0x02;

    private final Context ctx;
    private final Listener out;
    private final Executor exec;
    private BluetoothHidDevice hid;
    private BluetoothDevice host;
    private boolean registered;
    private String state = "off";

    Clicker(Context ctx, Listener out) {
        this.ctx = ctx;
        this.out = out;
        this.exec = ctx.getMainExecutor();
    }

    static boolean supported(Context c) {
        return Build.VERSION.SDK_INT >= Build.VERSION_CODES.P && adapter(c) != null;
    }

    private static BluetoothAdapter adapter(Context c) {
        BluetoothManager m = c.getSystemService(BluetoothManager.class);
        return m == null ? null : m.getAdapter();
    }

    boolean connected() {
        return host != null;
    }

    /** Registers the keyboard; the state arrives through the listener. */
    void start() {
        BluetoothAdapter a = adapter(ctx);
        if (a == null) {
            set("unsupported");
            return;
        }
        if (!a.isEnabled()) {
            set("btoff");
            return;
        }
        if (hid != null) {
            register();
            return;
        }
        set("starting");
        boolean ok = a.getProfileProxy(ctx, new BluetoothProfile.ServiceListener() {
            @Override
            public void onServiceConnected(int profile, BluetoothProfile proxy) {
                hid = (BluetoothHidDevice) proxy;
                register();
            }

            @Override
            public void onServiceDisconnected(int profile) {
                hid = null;
                host = null;
                registered = false;
                set("off");
            }
        }, BluetoothProfile.HID_DEVICE);
        if (!ok) set("unsupported");
    }

    private void register() {
        if (hid == null) return;
        if (registered) {
            set(host != null ? "connected" : "ready");
            return;
        }
        BluetoothHidDeviceAppSdpSettings sdp = new BluetoothHidDeviceAppSdpSettings(
                "Waymark", "Waymark slide clicker", "Waymark",
                BluetoothHidDevice.SUBCLASS1_KEYBOARD, DESCRIPTOR);
        if (!hid.registerApp(sdp, null, null, exec, callback)) set("unsupported");
    }

    private final BluetoothHidDevice.Callback callback = new BluetoothHidDevice.Callback() {
        @Override
        public void onAppStatusChanged(BluetoothDevice plugged, boolean reg) {
            registered = reg;
            if (!reg) host = null;
            set(reg ? (host != null ? "connected" : "ready") : "off");
        }

        @Override
        public void onConnectionStateChanged(BluetoothDevice d, int st) {
            if (st == BluetoothProfile.STATE_CONNECTED) {
                host = d;
                set("connected");
            } else if (st == BluetoothProfile.STATE_CONNECTING) {
                set("connecting");
            } else if (st == BluetoothProfile.STATE_DISCONNECTED && (host == null || host.equals(d))) {
                host = null;
                set(registered ? "ready" : "off");
            }
        }
    };

    /** Paired devices, computers first: [{a: address, n: name}]. */
    String hosts() {
        BluetoothAdapter a = adapter(ctx);
        JSONArray arr = new JSONArray();
        if (a == null) return arr.toString();
        List<BluetoothDevice> pcs = new ArrayList<>(), rest = new ArrayList<>();
        for (BluetoothDevice d : a.getBondedDevices()) {
            BluetoothClass c = d.getBluetoothClass();
            (c != null && c.getMajorDeviceClass() == BluetoothClass.Device.Major.COMPUTER ? pcs : rest).add(d);
        }
        pcs.addAll(rest);
        try {
            for (BluetoothDevice d : pcs) {
                arr.put(new JSONObject().put("a", d.getAddress()).put("n", d.getName() == null ? "" : d.getName()));
            }
        } catch (JSONException ignored) {
        }
        return arr.toString();
    }

    boolean connect(String address) {
        BluetoothAdapter a = adapter(ctx);
        if (hid == null || !registered || a == null || !BluetoothAdapter.checkBluetoothAddress(address)) return false;
        set("connecting");
        boolean ok = hid.connect(a.getRemoteDevice(address));
        if (!ok) set("ready");
        return ok;
    }

    /** Presses and releases one key: next, prev, black, start, end. */
    boolean key(String name) {
        if (hid == null || host == null) return false;
        int mod = 0, code;
        switch (name) {
            case "next": code = 0x4E; break;   // Page Down
            case "prev": code = 0x4B; break;   // Page Up
            case "black": code = 0x05; break;  // B
            case "start": code = 0x3E; break;  // F5
            case "here": mod = SHIFT; code = 0x3E; break; // Shift+F5: from this slide
            case "end": code = 0x29; break;    // Esc
            default: return false;
        }
        byte[] down = new byte[8];
        down[0] = (byte) mod;
        down[2] = (byte) code;
        boolean ok = hid.sendReport(host, REPORT_ID, down);
        hid.sendReport(host, REPORT_ID, new byte[8]);
        return ok;
    }

    void stop() {
        if (hid != null) {
            if (host != null) hid.disconnect(host);
            if (registered) hid.unregisterApp();
            BluetoothAdapter a = adapter(ctx);
            if (a != null) a.closeProfileProxy(BluetoothProfile.HID_DEVICE, hid);
        }
        hid = null;
        host = null;
        registered = false;
        set("off");
    }

    String state() {
        JSONObject o = new JSONObject();
        try {
            o.put("state", state);
            if (host != null) o.put("host", host.getName() == null ? host.getAddress() : host.getName());
        } catch (JSONException ignored) {
        }
        return o.toString();
    }

    void set(String s) {
        state = s;
        out.onState(state());
    }
}
