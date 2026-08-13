package com.photosmove.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.net.ConnectivityManager;
import android.net.LinkAddress;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.NetworkRequest;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.IBinder;
import android.os.PowerManager;
import android.util.Log;

import java.io.BufferedReader;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.Inet4Address;
import java.net.InetAddress;
import java.util.ArrayList;

public class ServerService extends Service {

    private static final String TAG = "photosmove";
    public static final String VERSION = BuildConfig.VERSION;
    private static final String CHANNEL_ID = "photosmove_server";
    private static final int NOTIFICATION_ID = 1;

    // Broadcast action and keys
    public static final String ACTION_UPDATE = "com.photosmove.app.UPDATE";
    public static final String EXTRA_STATUS = "status";
    public static final String EXTRA_PIN = "pin";
    public static final String EXTRA_URL = "url";
    public static final String EXTRA_PROGRESS = "progress";
    public static final String EXTRA_TRANSFER_STATE = "transfer_state";
    public static final String STATE_DONE = "done";
    public static final String STATE_CANCELLED = "cancelled";
    public static final String STATE_PAUSED = "paused";

    private Process serverProcess;
    private Thread serverThread;
    private InputStream serverStdout;
    private MediaFileServer mediaFileServer;
    private String detectedPin = "";
    private String detectedUrl = "";
    private WifiManager.WifiLock wifiLock;
    private PowerManager.WakeLock cpuWakeLock;
    private ConnectivityManager.NetworkCallback networkCallback;
    private volatile boolean serverStarted = false;
    private volatile boolean shutdownRequested = false;
    private volatile boolean wifiConnected = false;
    private long lastNotificationTime = 0;

    // Static last state for Activity to read on reconnect
    private static volatile String lastStatus = "starting";
    private static volatile String lastPin = "";
    private static volatile String lastUrl = "";
    // Running instance: after MainActivity switches language, it calls
    // refreshNotificationLocale to rebuild the notification with a wrapped context,
    // so notification text follows language switches on API 26-32 at runtime (D7)
    // without restarting the Service.
    private static volatile ServerService instance = null;
    // Context carrying the currently effective locale (updated by
    // refreshNotificationLocale on language switch). All notif getString calls go
    // through this context instead of the frozen `this`, so PROGRESS/DONE/... text
    // also follows a runtime language switch (review round 3: the old implementation
    // rebuilt once in refreshNotificationLocale, then got overwritten by the next
    // line's this.getString).
    private volatile Context localizedCtx = null;

    public static String getLastStatus() { return lastStatus; }
    public static String getLastPin() { return lastPin; }
    public static String getLastUrl() { return lastUrl; }

    @Override
    protected void attachBaseContext(Context base) {
        // Consistent with App/MainActivity: wrap the Service context when a language is
        // manually chosen on API 26-32, so getString(R.string.notif_*) follows (on API 33+
        // LocaleManager takes over app-wide and wrap is a no-op).
        super.attachBaseContext(LocaleHelper.wrap(base));
        // Initialize localizedCtx = context with the currently effective locale;
        // updated by refreshNotificationLocale on runtime language switch.
        localizedCtx = LocaleHelper.resolveLocalizedContext(this);
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        instance = this;
        createNotificationChannel();
        Intent notifIntent = new Intent(this, MainActivity.class);
        PendingIntent pi = PendingIntent.getActivity(this, 0, notifIntent,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
        Notification notification = new Notification.Builder(this, CHANNEL_ID)
                .setContentTitle(localizedCtx.getString(R.string.app_name))
                .setContentText(localizedCtx.getString(R.string.notif_service_running))
                .setSmallIcon(R.mipmap.ic_notification)
                .setOngoing(true)
                .setPriority(Notification.PRIORITY_MAX)
                .setDefaults(0)
                .setContentIntent(pi)
                .build();
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
        } else {
            startForeground(NOTIFICATION_ID, notification);
        }

        if (!serverStarted) {
            serverStarted = true;
            WifiManager wm = (WifiManager) getApplicationContext().getSystemService(WIFI_SERVICE);
            if (wm != null) {
                wifiLock = wm.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "photosmove");
                wifiLock.acquire();
            }

            PowerManager pm = (PowerManager) getApplicationContext().getSystemService(POWER_SERVICE);
            if (pm != null) {
                // SCREEN_DIM_WAKE_LOCK: keeps the screen on but dimmed, preventing
                // HyperOS/MIUI from killing the FGS as idle when the screen turns off
                // (PARTIAL_WAKE_LOCK only keeps the CPU; 60s after screen-off SmartPower
                // still cancels the FGS notification + stops the service).
                // Deprecated but still functional; the only workaround for HyperOS
                // background restrictions.
                cpuWakeLock = pm.newWakeLock(
                        PowerManager.SCREEN_DIM_WAKE_LOCK | PowerManager.ON_AFTER_RELEASE,
                        "photosmove::ScreenWakeLock");
                cpuWakeLock.acquire();
            }

            new Thread(this::runServer).start();
        }
        return START_STICKY;
    }

    private void createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= 26) {
            NotificationChannel channel = new NotificationChannel(
                    CHANNEL_ID, localizedCtx.getString(R.string.notif_channel_name), NotificationManager.IMPORTANCE_HIGH);
            channel.setSound(null, null);
            channel.setShowBadge(true);
            getSystemService(NotificationManager.class).createNotificationChannel(channel);
        }
    }

    private void broadcast(String status, String pin, String url) {
        broadcast(status, pin, url, null, null);
    }

    private void broadcast(String status, String pin, String url, String progress) {
        broadcast(status, pin, url, progress, null);
    }

    private void broadcast(String status, String pin, String url, String progress, String transferState) {
        if (status != null) lastStatus = status;
        if (pin != null && !pin.isEmpty()) lastPin = pin;
        if (url != null && !url.isEmpty()) lastUrl = url;
        Intent intent = new Intent(ACTION_UPDATE);
        intent.setPackage(getPackageName());
        if (status != null) intent.putExtra(EXTRA_STATUS, status);
        if (pin != null) intent.putExtra(EXTRA_PIN, pin);
        if (url != null) intent.putExtra(EXTRA_URL, url);
        if (progress != null) intent.putExtra(EXTRA_PROGRESS, progress);
        if (transferState != null) intent.putExtra(EXTRA_TRANSFER_STATE, transferState);
        sendBroadcast(intent);
    }

    @SuppressWarnings("deprecation")
    private String getWifiIP() {
        // Enumerate all networks and pick the IPv4 on a Wi-Fi transport. Do NOT use
        // getActiveNetwork(): when a VPN / mobile data / virtual interface is the system
        // default it returns that interface's address (e.g. 10.x), which is not on the Wi-Fi
        // segment and misleads the PC client. WifiManager.getIpAddress() is unreliable on
        // Android 12+ (returns 0) and is kept only as a last resort.
        try {
            ConnectivityManager cm = (ConnectivityManager)
                    getApplicationContext().getSystemService(CONNECTIVITY_SERVICE);
            if (cm != null) {
                String ip = wifiIPv4(cm);
                if (ip != null) return ip;
            }
        } catch (Exception e) {
            Log.e(TAG, "getWifiIP (ConnectivityManager) failed", e);
        }
        // fallback: WifiManager.getIpAddress (old devices / edge cases)
        try {
            WifiManager wm = (WifiManager) getApplicationContext().getSystemService(WIFI_SERVICE);
            if (wm != null) {
                int ip = wm.getConnectionInfo().getIpAddress();
                if (ip != 0) {
                    return String.format("%d.%d.%d.%d",
                            ip & 0xff, (ip >> 8) & 0xff, (ip >> 16) & 0xff, (ip >> 24) & 0xff);
                }
            }
        } catch (Exception e) {
            Log.e(TAG, "getWifiIP (WifiManager fallback) failed", e);
        }
        return null;
    }

    // Extract the first IPv4 non-loopback address from LinkProperties, or null if none.
    private String extractIpv4(LinkProperties lp) {
        if (lp == null) return null;
        for (LinkAddress addr : lp.getLinkAddresses()) {
            InetAddress inet = addr.getAddress();
            if (!inet.isLoopbackAddress() && (inet instanceof Inet4Address)) {
                return inet.getHostAddress();
            }
        }
        return null;
    }

    // Find the IPv4 of a Wi-Fi network by iterating all networks (not getActiveNetwork) and
    // requiring TRANSPORT_WIFI while excluding TRANSPORT_VPN, so a VPN-over-WiFi setup does not
    // return the tun interface's address. Shared by getWifiIP and the initial NetworkCallback
    // check so cold start and Wi-Fi refresh use one consistent source of truth.
    private String wifiIPv4(ConnectivityManager cm) {
        for (Network network : cm.getAllNetworks()) {
            NetworkCapabilities caps = cm.getNetworkCapabilities(network);
            if (caps == null) continue;
            if (!caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) continue;
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) continue;
            String ip = extractIpv4(cm.getLinkProperties(network));
            if (ip != null) return ip;
        }
        return null;
    }

    // Register a NetworkCallback listening for WiFi connect/disconnect to auto-update the URL (no app restart needed).
    // Callbacks fire on a system binder thread; broadcast/updateNotification are thread-safe.
    private void registerWifiCallback() {
        try {
            ConnectivityManager cm = (ConnectivityManager)
                    getApplicationContext().getSystemService(CONNECTIVITY_SERVICE);
            if (cm == null) {
                Log.w(TAG, "ConnectivityManager unavailable, skipping WiFi callback");
                return;
            }

            NetworkRequest req = new NetworkRequest.Builder()
                    .addTransportType(NetworkCapabilities.TRANSPORT_WIFI)
                    .build();

            networkCallback = new ConnectivityManager.NetworkCallback() {
                @Override
                public void onAvailable(Network network) {
                    // Some ROMs fire onAvailable but never re-fire onLinkPropertiesChanged for a
                    // Wi-Fi that was already connected at register time. Pull LinkProperties
                    // explicitly so the URL is captured as early as possible.
                    LinkProperties lp = cm.getLinkProperties(network);
                    if (lp != null) onLinkPropertiesChanged(network, lp);
                }

                @Override
                public void onLinkPropertiesChanged(Network network, LinkProperties lp) {
                    String ip = extractIpv4(lp);
                    if (ip != null) {
                        wifiConnected = true;
                        String url = "http://" + ip + ":8080";
                        Log.i(TAG, "WiFi IP (callback): " + ip);
                        detectedUrl = url;
                        broadcast("running", detectedPin, url);
                        updateNotification(url);
                    }
                }

                @Override
                public void onLost(Network network) {
                    wifiConnected = false;
                    Log.i(TAG, "WiFi network lost → wifi_required");
                    broadcast("wifi_required", null, null);
                    updateNotification(localizedCtx.getString(R.string.notif_wifi_required));
                }
            };

            cm.registerNetworkCallback(req, networkCallback);

            // Initial check: if Wi-Fi is already connected, broadcast its real URL immediately
            // instead of only flipping a boolean and waiting for onLinkPropertiesChanged (which
            // is not guaranteed to re-fire for an already-connected network on every ROM — that
            // left the cold-start URL stuck on a wrong/0.0.0.0 value). If no Wi-Fi, prompt.
            String initialIp = wifiIPv4(cm);
            if (initialIp != null) {
                wifiConnected = true;
                String url = "http://" + initialIp + ":8080";
                Log.i(TAG, "WiFi IP (initial): " + initialIp);
                detectedUrl = url;
                broadcast("running", detectedPin, url);
                updateNotification(url);
            } else {
                Log.i(TAG, "No active WiFi at startup → wifi_required");
                broadcast("wifi_required", null, null);
                updateNotification(localizedCtx.getString(R.string.notif_wifi_required));
            }
        } catch (Exception e) {
            Log.e(TAG, "registerNetworkCallback failed", e);
        }
    }

    private void updateNotification(String text) {
        updateNotification(text, true);
    }

    private void updateNotification(String text, boolean throttle) {
        long now = System.currentTimeMillis();
        if (throttle && now - lastNotificationTime < 2000) return;
        lastNotificationTime = now;

        NotificationManager nm = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        Intent notifIntent = new Intent(this, MainActivity.class);
        PendingIntent pi = PendingIntent.getActivity(this, 0, notifIntent,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
        Notification notification = new Notification.Builder(this, CHANNEL_ID)
                .setContentTitle(localizedCtx.getString(R.string.app_name))
                .setContentText(text)
                .setSmallIcon(R.mipmap.ic_notification)
                .setOngoing(true)
                .setPriority(Notification.PRIORITY_MAX)
                .setDefaults(0)
                .setContentIntent(pi)
                .build();
        nm.notify(NOTIFICATION_ID, notification);
    }

    /**
     * Called by MainActivity after a runtime language switch: updates localizedCtx to a context
     * with the new locale and rebuilds the current notification, so subsequent PROGRESS/DONE/...
     * updateNotification(localizedCtx.getString) calls also follow (D7).
     * On API 33+ LocaleManager covers the app level and resolveLocalizedContext returns base,
     * but the already-shown notification still needs rebuilding.
     */
    public static void refreshNotificationLocale(Context appCtx) {
        ServerService svc = instance;
        if (svc == null) return;
        // Use svc rather than appCtx: on API 26-32 resolveLocalizedContext overrides the locale
        // (base-independent, equivalent); on API 33+ it returns base, and svc (Service context)
        // unambiguously follows LocaleManager, whereas appCtx (the old Activity) may have a
        // config lagging behind setApplicationLocales before recreate.
        svc.localizedCtx = LocaleHelper.resolveLocalizedContext(svc);
        svc.doRefreshNotification(svc.localizedCtx);
    }

    private void doRefreshNotification(Context ctx) {
        NotificationManager nm = (NotificationManager) ctx.getSystemService(NOTIFICATION_SERVICE);
        if (nm == null) return;
        Intent notifIntent = new Intent(this, MainActivity.class);
        PendingIntent pi = PendingIntent.getActivity(this, 0, notifIntent,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
        // Rebuild the static notification text; if a transfer is in progress, subsequent PROGRESS
        // updates already follow the new language via localizedCtx.getString.
        String text = "wifi_required".equals(lastStatus)
                ? ctx.getString(R.string.notif_wifi_required)
                : ctx.getString(R.string.notif_service_running);
        Notification notification = new Notification.Builder(ctx, CHANNEL_ID)
                .setContentTitle(ctx.getString(R.string.app_name))
                .setContentText(text)
                .setSmallIcon(R.mipmap.ic_notification)
                .setOngoing(true)
                .setPriority(Notification.PRIORITY_MAX)
                .setDefaults(0)
                .setContentIntent(pi)
                .build();
        nm.notify(NOTIFICATION_ID, notification);
    }

    private void runServer() {
        serverThread = Thread.currentThread();
        try {
            File filesDir = getFilesDir();
            File webDir = new File(filesDir, "web");

            copyAssetDir("web", webDir);

            broadcast("starting", null, null);

            File mediaStoreJson = new File(filesDir, "media_store.json");
            try {
                MediaStoreScanner.scan(ServerService.this, mediaStoreJson);
            } catch (Exception e) {
                Log.e(TAG, "MediaStore scan failed", e);
            }

            mediaFileServer = new MediaFileServer(8082);
            mediaFileServer.start();

            String nativeLibDir = getApplicationInfo().nativeLibraryDir;
            File binary = new File(nativeLibDir, "libphotosmove.so");

            // getWifiIP is read once; on failure -lan-ip is not passed — the Go side's getLANIP
            // falls back to UDP probing (net.Dial consults the routing table; more robust than
            // WifiManager.getIpAddress and independent of WiFi readiness).
            // No longer blocks startup with retries (the previous 10s loop slowed app startup).
            String wifiIP = getWifiIP();
            Log.i(TAG, "photosmove v" + VERSION + " starting");
            Log.i(TAG, "WiFi IP: " + wifiIP);

            ArrayList<String> cmd = new ArrayList<>();
            cmd.add(binary.getAbsolutePath());
            cmd.add("-roots"); cmd.add("/sdcard/DCIM/Camera");
            cmd.add("-web"); cmd.add(webDir.getAbsolutePath());
            cmd.add("-port"); cmd.add("8080");
            if (wifiIP != null && !wifiIP.isEmpty()) {
                cmd.add("-lan-ip"); cmd.add(wifiIP);
            }
            cmd.add("-media-db"); cmd.add(mediaStoreJson.getAbsolutePath());
            cmd.add("-media-port"); cmd.add("8082");
            ProcessBuilder pb = new ProcessBuilder(cmd);
            pb.redirectErrorStream(true);
            serverProcess = pb.start();
            serverStdout = serverProcess.getInputStream();

            // Register NetworkCallback: WiFi lost → prompt, WiFi connected → auto-update URL (no app restart needed).
            registerWifiCallback();

            BufferedReader reader = new BufferedReader(
                    new InputStreamReader(serverStdout));
            String line;

            while ((line = reader.readLine()) != null) {
                Log.i(TAG, line);

                // Parse progress lines: PROGRESS:batchID sent total pct files fileName
                if (line.contains("PROGRESS:")) {
                    int idx = line.indexOf("PROGRESS:");
                    String payload = line.substring(idx + 9).trim();
                    String[] parts = payload.split(" ", 6);
                    if (parts.length >= 4) {
                        try {
                            long sent = Long.parseLong(parts[1]);
                            long total = Long.parseLong(parts[2]);
                            long pct = Long.parseLong(parts[3]);
                            String file = parts.length > 5 ? parts[5] : "";
                            String progressText = formatProgress(sent, total, pct, file);
                            updateNotification(localizedCtx.getString(R.string.notif_transferring, pct, formatSize(sent), formatSize(total)));
                            broadcast(null, null, null, progressText);
                        } catch (NumberFormatException ignored) {}
                    }
                    continue;
                }

                if (line.contains("DONE:")) {
                    int idx = line.indexOf("DONE:");
                    String payload = line.substring(idx + 5).trim();
                    String[] parts = payload.split(" ", 2);
                    String sizeStr = parts.length > 1 ? formatSize(Long.parseLong(parts[1])) : "";
                    updateNotification(localizedCtx.getString(R.string.notif_done, sizeStr), false);
                    broadcast(null, null, null, null, STATE_DONE);
                    continue;
                }

                if (line.contains("CANCELLED:")) {
                    updateNotification(localizedCtx.getString(R.string.notif_cancelled), false);
                    broadcast(null, null, null, null, STATE_CANCELLED);
                    continue;
                }

                if (line.contains("PAUSED:")) {
                    updateNotification(localizedCtx.getString(R.string.notif_paused), false);
                    broadcast(null, null, null, null, STATE_PAUSED);
                    continue;
                }

                if (line.contains("PIN:")) {
                    int pinIdx = line.indexOf("PIN:");
                    detectedPin = line.substring(pinIdx + 4).trim();
                    broadcast(null, detectedPin, null);
                }

                if (line.contains("listening on")) {
                    int idx = line.indexOf("https://");
                    if (idx < 0) idx = line.indexOf("http://");
                    if (idx >= 0) {
                        detectedUrl = line.substring(idx);
                    }
                    // Only trust Go's URL when Wi-Fi is actually connected. Without Wi-Fi, Go's
                    // getLANIP follows the default route and may print a cellular/VPN egress IP
                    // (not 0.0.0.0) that a PC cannot reach — broadcasting it would flip the UI
                    // from the correct wifi_required prompt to a misleading running URL.
                    if (!detectedUrl.contains("0.0.0.0") && wifiConnected) {
                        broadcast("running", detectedPin, detectedUrl);
                    }
                }
            }

            int exitCode = serverProcess.waitFor();
            broadcast("stopped", null, null);

        } catch (Exception e) {
            if (shutdownRequested) {
                Log.i(TAG, "Server shutdown requested, ignoring interrupt");
            } else {
                Log.e(TAG, "Server error", e);
                broadcast("error", null, null);
            }
        } finally {
            serverStarted = false;
            if (!shutdownRequested) {
                stopSelf();
            }
        }
    }

    private void copyAsset(String assetName, File dest, boolean force) throws Exception {
        long apkTime = new File(getApplicationInfo().sourceDir).lastModified();
        if (!force && dest.exists() && dest.lastModified() >= apkTime) {
            return;
        }
        dest.getParentFile().mkdirs();
        try (InputStream is = getAssets().open(assetName);
             OutputStream os = new FileOutputStream(dest)) {
            byte[] buf = new byte[65536];
            int len;
            while ((len = is.read(buf)) > 0) {
                os.write(buf, 0, len);
            }
        }
    }

    private void copyAssetDir(String assetPath, File destDir) throws Exception {
        if (!destDir.exists()) {
            destDir.mkdirs();
        }
        String[] files = getAssets().list(assetPath);
        if (files == null) return;
        for (String file : files) {
            String assetFilePath = assetPath + "/" + file;
            String[] subFiles = getAssets().list(assetFilePath);
            if (subFiles != null && subFiles.length > 0) {
                copyAssetDir(assetFilePath, new File(destDir, file));
            } else {
                File dest = new File(destDir, file);
                copyAsset(assetFilePath, dest, false);
            }
        }
    }

    @Override
    public void onDestroy() {
        super.onDestroy();
        instance = null;
        shutdownRequested = true;
        if (networkCallback != null) {
            try {
                ConnectivityManager cm = (ConnectivityManager)
                        getApplicationContext().getSystemService(CONNECTIVITY_SERVICE);
                if (cm != null) cm.unregisterNetworkCallback(networkCallback);
            } catch (Exception ignored) {}
            networkCallback = null;
        }
        if (wifiLock != null && wifiLock.isHeld()) {
            wifiLock.release();
        }
        if (cpuWakeLock != null && cpuWakeLock.isHeld()) {
            cpuWakeLock.release();
        }
        if (serverStdout != null) {
            try { serverStdout.close(); } catch (Exception ignored) {}
        }
        if (serverProcess != null) {
            serverProcess.destroy();
            try {
                if (!serverProcess.waitFor(5, java.util.concurrent.TimeUnit.SECONDS)) {
                    serverProcess.destroyForcibly();
                }
            } catch (InterruptedException ignored) {}
        }
        if (mediaFileServer != null) {
            mediaFileServer.shutdown();
        }
    }

    private static String formatSize(long bytes) {
        if (bytes < 1024) return bytes + "B";
        if (bytes < 1024 * 1024) return String.format("%.1fKB", bytes / 1024.0);
        if (bytes < 1024L * 1024 * 1024) return String.format("%.1fMB", bytes / (1024.0 * 1024));
        return String.format("%.1fGB", bytes / (1024.0 * 1024 * 1024));
    }

    private static String formatProgress(long sent, long total, long pct, String file) {
        StringBuilder sb = new StringBuilder();
        sb.append(pct).append("%");
        sb.append(" · ").append(formatSize(sent));
        if (total > 0) sb.append("/").append(formatSize(total));
        if (!file.isEmpty()) sb.append(" · ").append(file);
        return sb.toString();
    }
}
