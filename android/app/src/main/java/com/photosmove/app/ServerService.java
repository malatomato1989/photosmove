package com.photosmove.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.graphics.drawable.Icon;
import android.net.ConnectivityManager;
import android.net.LinkAddress;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.NetworkRequest;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;
import android.os.PowerManager;
import android.util.Log;
import android.widget.Toast;

import java.io.BufferedReader;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.Inet4Address;
import java.net.InetAddress;
import java.util.ArrayList;
import java.util.Locale;

public class ServerService extends Service {

    private static final String TAG = "photosmove";
    public static final String VERSION = BuildConfig.VERSION;
    private static final String CHANNEL_ID = "photosmove_server";
    private static final int NOTIFICATION_ID = 1;
    private static final int REQUEST_CONTENT = 0;
    private static final int REQUEST_STOP = 1;

    // Broadcast action and keys
    public static final String ACTION_UPDATE = "com.photosmove.app.UPDATE";
    public static final String ACTION_STOP = "com.photosmove.app.STOP";
    public static final String EXTRA_STATUS = "status";
    public static final String EXTRA_PIN = "pin";
    public static final String EXTRA_URL = "url";
    public static final String EXTRA_PROGRESS = "progress";
    public static final String EXTRA_TRANSFER_STATE = "transfer_state";
    public static final String EXTRA_ALBUMS = "albums";
    public static final String EXTRA_FILES = "files";
    public static final String EXTRA_TOTAL_SIZE = "total_size";
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
    private volatile ConnectivityManager.NetworkCallback networkCallback;
    private volatile boolean serverStarted = false;
    private volatile boolean shutdownRequested = false;
    // Invalidates the previous runServer thread when stop → start happens quickly: the old
    // thread must not clear serverStarted / stopSelf / tear down the new run.
    private volatile int runGeneration = 0;
    // Serializes start/finish/stop state transitions with the background run's finally block.
    private final Object lifecycleLock = new Object();
    // startId of the run that owns `serverStarted`; guarded by lifecycleLock.
    private int currentStartId = -1;
    private volatile String lastNotificationText = "";
    private volatile boolean wifiConnected = false;
    private long lastNotificationTime = 0;

    // Static last state for Activity to read on reconnect
    private static volatile String lastStatus = "starting";
    private static volatile String lastPin = "";
    private static volatile String lastUrl = "";
    // Library summary (album count / file count / total bytes), computed after the MediaStore
    // scan and shown on the phone UI. 0 means "not available yet".
    private static volatile long lastAlbums = 0;
    private static volatile long lastFiles = 0;
    private static volatile long lastTotalSize = 0;
    // Real server start epoch (set once when the server thread starts). Lets the Activity compute
    // uptime from the Service's actual start so the counter survives Activity recreation (e.g. the
    // system reclaiming the task while the foreground Service keeps running) instead of resetting.
    private static volatile long serverStartEpoch = 0;
    // Actual port picked by the Go process (0 = not reported yet). All URL building must
    // go through buildUrl() so a fallback port never surfaces as the hardcoded default.
    private volatile int serverPort = 0;
    private volatile String wifiIp = null;
    // Reuse the last successful port on the next run to minimize URL changes.
    private static final String PREFS_NAME = "photosmove_prefs";
    private static final String KEY_LAST_PORT = "last_port";
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
    public static long getLastAlbums() { return lastAlbums; }
    public static long getLastFiles() { return lastFiles; }
    public static long getLastTotalSize() { return lastTotalSize; }
    public static long getLastStartEpoch() { return serverStartEpoch; }

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
        if (intent != null && ACTION_STOP.equals(intent.getAction())) {
            stopServerAndSelf(startId);
            return START_NOT_STICKY;
        }

        instance = this;
        final int startNewGeneration;
        final boolean startNew;
        synchronized (lifecycleLock) {
            // A fresh start (including stop → start) must never inherit the previous stop flag.
            shutdownRequested = false;
            // Track the latest delivered startId (even for rebroadcast-only starts) so the
            // run's finally can stopSelf with the newest id and never strand a dead service.
            currentStartId = startId;
            startNew = !serverStarted;
            if (startNew) {
                serverStarted = true;
                startNewGeneration = ++runGeneration;
            } else {
                startNewGeneration = runGeneration;
            }
        }

        createNotificationChannel();
        // Repeated starts while already running must not overwrite a progress/done/wifi
        // notification with the generic "service running" text.
        String notifText = startNew
                ? localizedCtx.getString(R.string.notif_service_running)
                : lastNotificationText;
        if (notifText == null || notifText.isEmpty()) {
            notifText = localizedCtx.getString(R.string.notif_service_running);
        }
        lastNotificationText = notifText;
        Notification notification = buildNotification(
                localizedCtx.getString(R.string.app_name), notifText);
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
        } else {
            startForeground(NOTIFICATION_ID, notification);
        }

        if (startNew) {
            final int generation = startNewGeneration;
            serverStartEpoch = System.currentTimeMillis();
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

            new Thread(() -> runServer(generation)).start();
        } else {
            // Service is already running but startForegroundService was triggered again (e.g.
            // MainActivity re-granted storage permission and called startServer). onStartCommand
            // is normally a no-op here, so the recreated Activity would never receive URL/PIN.
            // Re-broadcast the current state so the reconnecting UI syncs immediately.
            rebroadcastState();
        }
        return START_STICKY;
    }

    private PendingIntent contentPendingIntent() {
        Intent notifIntent = new Intent(this, MainActivity.class);
        // Reuse the existing task instead of stacking another MainActivity: tapping the
        // ongoing notification repeatedly used to require multiple back presses.
        notifIntent.setFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP | Intent.FLAG_ACTIVITY_SINGLE_TOP);
        return PendingIntent.getActivity(this, REQUEST_CONTENT, notifIntent,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    }

    private PendingIntent stopPendingIntent() {
        Intent stopIntent = new Intent(this, ServerService.class);
        stopIntent.setAction(ACTION_STOP);
        return PendingIntent.getService(this, REQUEST_STOP, stopIntent,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    }

    // Single notification builder: same ID + setOnlyAlertOnce prevents ROMs from popping a
    // fresh heads-up alert on every progress update / repeat start.
    private Notification buildNotification(String title, String text) {
        Notification.Builder builder = new Notification.Builder(this, CHANNEL_ID)
                .setContentTitle(title)
                .setContentText(text)
                .setSmallIcon(R.drawable.ic_notification)
                .setOngoing(true)
                .setOnlyAlertOnce(true)
                .setPriority(Notification.PRIORITY_MAX)
                .setDefaults(0)
                .setContentIntent(contentPendingIntent())
                .addAction(new Notification.Action.Builder(
                        Icon.createWithResource(this, R.drawable.ic_stop),
                        localizedCtx.getString(R.string.notif_stop_action),
                        stopPendingIntent()).build())
                .setSound(null)
                .setVibrate(null);
        return builder.build();
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

    // Re-broadcast the cached state so a reconnecting Activity syncs without waiting for a fresh
    // lifecycle event from the server thread. Only forward URL/PIN for the "running" state: for
    // wifi_required/stopped/error the receiver must hide the URL card, and broadcasting a stale
    // or 0.0.0.0 URL here would re-show it (the receiver unhides the card for any non-empty url).
    // Read only the volatile last* fields — not the non-volatile detected* — to avoid a
    // cross-thread stale read from the main thread.
    private void rebroadcastState() {
        boolean running = "running".equals(lastStatus);
        broadcast(lastStatus, running ? lastPin : null, running ? lastUrl : null);
    }

    private void broadcast(String status, String pin, String url) {
        broadcast(status, pin, url, null, null);
    }

    private void broadcast(String status, String pin, String url, String progress) {
        broadcast(status, pin, url, progress, null);
    }

    private void broadcast(String status, String pin, String url, String progress, String transferState) {
        if (status != null) lastStatus = status;
        // starting/stopped/error have no usable address: drop cached URL/PIN so a recreated
        // Activity cannot restore a dead endpoint (broadcast only ever overwrites non-empty).
        if ("starting".equals(status) || "stopped".equals(status) || "error".equals(status)) {
            lastUrl = "";
            lastPin = "";
            detectedUrl = "";
            detectedPin = "";
            serverPort = 0;
        }
        if (pin != null && !pin.isEmpty()) lastPin = pin;
        if (url != null && !url.isEmpty()) lastUrl = url;
        Intent intent = new Intent(ACTION_UPDATE);
        intent.setPackage(getPackageName());
        if (status != null) intent.putExtra(EXTRA_STATUS, status);
        if (pin != null) intent.putExtra(EXTRA_PIN, pin);
        if (url != null) intent.putExtra(EXTRA_URL, url);
        if (progress != null) intent.putExtra(EXTRA_PROGRESS, progress);
        if (transferState != null) intent.putExtra(EXTRA_TRANSFER_STATE, transferState);
        // Album stats describe a usable library: do not let them reappear after stop/error
        // (the receiver unhides the library card whenever the extras are present).
        if (lastAlbums > 0 && (status == null || "running".equals(status) || "starting".equals(status))) {
            intent.putExtra(EXTRA_ALBUMS, lastAlbums);
            intent.putExtra(EXTRA_FILES, lastFiles);
            intent.putExtra(EXTRA_TOTAL_SIZE, lastTotalSize);
        }
        sendBroadcast(intent);
    }

    // Explicit user stop (in-app button or notification action). onDestroy performs the
    // remaining teardown; doing the time-sensitive pieces here gives immediate UI feedback.
    private void stopServerAndSelf(int startId) {
        synchronized (lifecycleLock) {
            shutdownRequested = true;
            serverStarted = false;
            runGeneration++;
            currentStartId = -1;
        }
        wifiConnected = false;
        wifiIp = null;
        unregisterWifiCallback();
        // Drop the library summary with the service so stopped/error states never show it.
        lastAlbums = 0;
        lastFiles = 0;
        lastTotalSize = 0;
        broadcast("stopped", null, null);
        NotificationManager nm = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        if (nm != null) nm.cancel(NOTIFICATION_ID);
        stopForeground(STOP_FOREGROUND_REMOVE);
        if (mediaFileServer != null) {
            mediaFileServer.shutdown();
        }
        if (serverProcess != null) {
            serverProcess.destroy();
        }
        // stopSelf(startId) is ignored if a newer start request arrived in the meantime,
        // so a rapid stop → start cannot kill the fresh run.
        stopSelf(startId);
    }

    private String buildUrl(String ip) {
        int port = serverPort;
        if (ip == null || ip.isEmpty() || port <= 0) return null;
        return "http://" + ip + ":" + port;
    }

    // Called when the Go child reports PORT:<n>. If Wi-Fi was already known before the port
    // arrived (startup race), publish the URL now; otherwise the network callback will.
    private void onPortReady(int port, int generation) {
        // Re-check generation: a stop → start can happen between reading the line and here.
        if (port <= 0 || isStale(generation)) return;
        serverPort = port;
        // Remember the successful port so the next start tries it first.
        getSharedPreferences(PREFS_NAME, MODE_PRIVATE).edit()
                .putInt(KEY_LAST_PORT, port).apply();
        if (wifiConnected) {
            String url = buildUrl(wifiIp);
            if (url != null) {
                detectedUrl = url;
                broadcast("running", detectedPin, url);
                updateNotification(url);
            }
        }
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

    private void unregisterWifiCallback() {
        ConnectivityManager.NetworkCallback callback = networkCallback;
        networkCallback = null;
        if (callback == null) return;
        try {
            ConnectivityManager cm = (ConnectivityManager)
                    getApplicationContext().getSystemService(CONNECTIVITY_SERVICE);
            if (cm != null) cm.unregisterNetworkCallback(callback);
        } catch (Exception ignored) {}
    }

    // Register a NetworkCallback listening for WiFi connect/disconnect to auto-update the URL (no app restart needed).
    // Callbacks fire on a system binder thread; broadcast/updateNotification are thread-safe.
    private void registerWifiCallback() {
        if (shutdownRequested) return;
        // Never stack callbacks across a stop → start on the same Service instance.
        unregisterWifiCallback();
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
                    if (shutdownRequested) return;
                    // Some ROMs fire onAvailable but never re-fire onLinkPropertiesChanged for a
                    // Wi-Fi that was already connected at register time. Pull LinkProperties
                    // explicitly so the URL is captured as early as possible.
                    LinkProperties lp = cm.getLinkProperties(network);
                    if (lp != null) onLinkPropertiesChanged(network, lp);
                }

                @Override
                public void onLinkPropertiesChanged(Network network, LinkProperties lp) {
                    if (shutdownRequested) return;
                    // Skip VPN networks: their caps include the underlying transports, so a
                    // VPN-over-Wi-Fi network satisfies the TRANSPORT_WIFI request, but its
                    // LinkProperties address is the tun IP (e.g. 172.18.x.x), not the Wi-Fi IP.
                    NetworkCapabilities caps = cm.getNetworkCapabilities(network);
                    if (caps == null || !caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)
                            || caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) return;
                    String ip = extractIpv4(lp);
                    if (ip != null) {
                        wifiConnected = true;
                        wifiIp = ip;
                        Log.i(TAG, "WiFi IP (callback): " + ip);
                        String url = buildUrl(ip);
                        // Port may not be reported yet during cold start; onPortReady() will
                        // publish the URL once PORT:<n> arrives.
                        if (url != null) {
                            detectedUrl = url;
                            broadcast("running", detectedPin, url);
                            updateNotification(url);
                        }
                    }
                }

                @Override
                public void onLost(Network network) {
                    if (shutdownRequested) return;
                    // A lost VPN network also satisfies the Wi-Fi NetworkRequest; do not let it
                    // masquerade as "Wi-Fi lost". Re-evaluate the actual Wi-Fi IPv4 so a late
                    // onLost for an old network cannot overwrite a newer valid address.
                    NetworkCapabilities caps = cm.getNetworkCapabilities(network);
                    if (caps != null && caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) return;
                    String ip = wifiIPv4(cm);
                    if (ip != null) {
                        boolean changed = !wifiConnected || !ip.equals(wifiIp);
                        wifiConnected = true;
                        wifiIp = ip;
                        if (changed) {
                            String url = buildUrl(ip);
                            if (url != null) {
                                detectedUrl = url;
                                broadcast("running", detectedPin, url);
                                updateNotification(url);
                            }
                        }
                        return;
                    }
                    wifiConnected = false;
                    wifiIp = null;
                    Log.i(TAG, "WiFi network lost → wifi_required");
                    broadcast("wifi_required", null, null);
                    updateNotification(localizedCtx.getString(R.string.notif_wifi_required));
                }
            };

            cm.registerNetworkCallback(req, networkCallback);

            if (shutdownRequested) return;
            // Initial check: if Wi-Fi is already connected, broadcast its real URL immediately
            // instead of only flipping a boolean and waiting for onLinkPropertiesChanged (which
            // is not guaranteed to re-fire for an already-connected network on every ROM — that
            // left the cold-start URL stuck on a wrong/0.0.0.0 value). If no Wi-Fi, prompt.
            String initialIp = wifiIPv4(cm);
            if (initialIp != null) {
                wifiConnected = true;
                wifiIp = initialIp;
                Log.i(TAG, "WiFi IP (initial): " + initialIp);
                String url = buildUrl(initialIp);
                if (url != null) {
                    detectedUrl = url;
                    broadcast("running", detectedPin, url);
                    updateNotification(url);
                }
            } else {
                wifiConnected = false;
                wifiIp = null;
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
        lastNotificationText = text;

        NotificationManager nm = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        Notification notification = buildNotification(
                localizedCtx.getString(R.string.app_name), text);
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
        if (svc == null || svc.shutdownRequested) return;
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
        // Rebuild the static notification text; if a transfer is in progress, subsequent PROGRESS
        // updates already follow the new language via localizedCtx.getString.
        String text = "wifi_required".equals(lastStatus)
                ? ctx.getString(R.string.notif_wifi_required)
                : ctx.getString(R.string.notif_service_running);
        lastNotificationText = text;
        Notification notification = buildNotification(ctx.getString(R.string.app_name), text);
        nm.notify(NOTIFICATION_ID, notification);
    }

    private boolean isStale(int generation) {
        return shutdownRequested || runGeneration != generation;
    }

    private void runServer(int generation) {
        serverThread = Thread.currentThread();
        // Locals so a stale run can never tear down a newer run's process/socket via the
        // shared fields during a stop → start race.
        MediaFileServer mediaServer = null;
        Process process = null;
        InputStream stdout = null;
        try {
            if (isStale(generation)) return;
            File filesDir = getFilesDir();
            File webDir = new File(filesDir, "web");

            // Fresh run: never trust in-memory port/WiFi from a previous run.
            serverPort = 0;
            wifiConnected = false;
            wifiIp = null;
            lastNotificationTime = 0;

            copyAssetDir("web", webDir);
            // copyAssetDir can be slow after an APK update; a stop → start may have happened.
            if (isStale(generation)) return;

            // A new run re-scans: clear the previous summary so broadcast("starting") and any
            // state restore during the scan cannot surface stale stats.
            lastAlbums = 0;
            lastFiles = 0;
            lastTotalSize = 0;
            broadcast("starting", null, null);

            File mediaStoreJson = new File(filesDir, "media_store.json");
            try {
                long[] stats = MediaStoreScanner.scan(ServerService.this, mediaStoreJson);
                if (stats != null && stats.length == 3) {
                    // The scan can take seconds on a large library; never let a stopped/old
                    // run repopulate the UI with its results.
                    if (isStale(generation)) return;
                    lastAlbums = stats[0];
                    lastFiles = stats[1];
                    lastTotalSize = stats[2];
                    Log.i(TAG, "Library: " + lastAlbums + " albums, " + lastFiles
                            + " files, " + formatSize(lastTotalSize));
                    // Push the summary to the UI (status unchanged → no side effects).
                    broadcast(null, null, null);
                }
            } catch (Exception e) {
                Log.e(TAG, "MediaStore scan failed", e);
            }

            if (isStale(generation)) return;

            // Ephemeral loopback port: avoids a second hardcoded port conflicting with another app.
            mediaServer = new MediaFileServer(0);
            mediaFileServer = mediaServer;
            mediaServer.start();
            int mediaPort = mediaServer.getPort();
            if (mediaPort <= 0) {
                if (isStale(generation)) return;
                throw new IllegalStateException("MediaFileServer failed to bind a port");
            }

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
            // Try last run's successful port first; Go then falls back through the easy
            // pool (1111...9999) and finally an ephemeral port, reporting PORT:<n>.
            int lastPort = getSharedPreferences(PREFS_NAME, MODE_PRIVATE).getInt(KEY_LAST_PORT, 0);
            if (lastPort > 0) {
                cmd.add("-port"); cmd.add(String.valueOf(lastPort));
            }
            if (wifiIP != null && !wifiIP.isEmpty()) {
                cmd.add("-lan-ip"); cmd.add(wifiIP);
            }
            cmd.add("-media-db"); cmd.add(mediaStoreJson.getAbsolutePath());
            cmd.add("-media-port"); cmd.add(String.valueOf(mediaPort));
            ProcessBuilder pb = new ProcessBuilder(cmd);
            pb.redirectErrorStream(true);
            process = pb.start();
            serverProcess = process;
            stdout = process.getInputStream();
            serverStdout = stdout;
            if (isStale(generation)) {
                process.destroy();
                return;
            }

            // Register NetworkCallback: WiFi lost → prompt, WiFi connected → auto-update URL (no app restart needed).
            registerWifiCallback();

            BufferedReader reader = new BufferedReader(
                    new InputStreamReader(stdout));
            String line;
            boolean bindFailed = false;

            while ((line = reader.readLine()) != null) {
                Log.i(TAG, line);
                // Explicit stop (or onDestroy) wins over any buffered child-process lines;
                // otherwise a late PORT/listening line could overwrite "stopped" with "running".
                if (isStale(generation)) break;

                if (!line.contains("PROGRESS:") && line.contains("ERROR:PORT_UNAVAILABLE")) {
                    bindFailed = true;
                    broadcast("error", null, null);
                    final String msg = localizedCtx.getString(R.string.port_allocate_failed);
                    new Handler(Looper.getMainLooper()).post(() ->
                            Toast.makeText(getApplicationContext(), msg, Toast.LENGTH_LONG).show());
                    continue;
                }

                // Guard against a PROGRESS line whose file name happens to contain "PORT:".
                if (!line.contains("PROGRESS:") && line.contains("PORT:")) {
                    int idx = line.indexOf("PORT:");
                    try {
                        onPortReady(Integer.parseInt(line.substring(idx + 5).trim()), generation);
                    } catch (NumberFormatException ignored) {}
                    continue;
                }

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
                    // Prefer the Wi-Fi address we validated ourselves. Go's getLANIP follows
                    // the default route and may be a cellular/VPN address when -lan-ip was not
                    // passed; use its printed URL only as a fallback.
                    String url = buildUrl(wifiIp);
                    if (url == null) {
                        int idx = line.indexOf("https://");
                        if (idx < 0) idx = line.indexOf("http://");
                        if (idx >= 0) url = line.substring(idx);
                    }
                    // Only publish when Wi-Fi is actually connected.
                    if (url != null && !url.contains("0.0.0.0") && wifiConnected) {
                        detectedUrl = url;
                        broadcast("running", detectedPin, url);
                        updateNotification(url);
                    }
                }
            }

            process.waitFor();
            // If the bind failed we already broadcast "error" + showed the reason; if the user
            // stopped the service, stopServerAndSelf already broadcast "stopped".
            if (!bindFailed && !isStale(generation)) {
                broadcast("stopped", null, null);
            }

        } catch (Exception e) {
            if (isStale(generation)) {
                Log.i(TAG, "Server shutdown requested, ignoring interrupt");
            } else {
                Log.e(TAG, "Server error", e);
                broadcast("error", null, null);
            }
        } finally {
            // Mark this run finished *before* the (potentially slow) cleanup so a start that
            // arrives during cleanup can immediately launch a fresh run.
            boolean shouldStop = false;
            synchronized (lifecycleLock) {
                if (runGeneration == generation) {
                    serverStarted = false;
                    shouldStop = !shutdownRequested;
                }
            }
            // Close this run's own resources even if a newer run already replaced the fields.
            if (mediaServer != null) mediaServer.shutdown();
            if (stdout != null) {
                try { stdout.close(); } catch (Exception ignored) {}
            }
            if (process != null) {
                process.destroy();
            }
            if (shouldStop) {
                synchronized (lifecycleLock) {
                    // Re-check under the lock: a newer start may have claimed the service while
                    // cleanup was running. stopSelf(latest) also refuses to kill a newer start.
                    if (runGeneration == generation && !shutdownRequested) {
                        stopSelf(currentStartId);
                    }
                }
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
        synchronized (lifecycleLock) {
            shutdownRequested = true;
            serverStarted = false;
            runGeneration++;
            currentStartId = -1;
        }
        // Let a reconnecting UI leave the stale "running" state, but never overwrite a real
        // startup error with "stopped".
        if (!"error".equals(lastStatus)) {
            broadcast("stopped", null, null);
        }
        // Service gone: drop the library summary so a later Activity cannot restore stale stats.
        lastAlbums = 0;
        lastFiles = 0;
        lastTotalSize = 0;
        unregisterWifiCallback();
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
                if (!serverProcess.waitFor(2, java.util.concurrent.TimeUnit.SECONDS)) {
                    serverProcess.destroyForcibly();
                }
            } catch (InterruptedException ignored) {}
        }
        if (mediaFileServer != null) {
            mediaFileServer.shutdown();
        }
    }

    public static String formatSize(long bytes) {
        // Kept identical to the web formatSize (1024-based, same precision/spacing) so the
        // phone UI and the PC browser show the same numbers.
        if (bytes < 1024) return bytes + " B";
        if (bytes < 1024 * 1024) return String.format(Locale.US, "%.1f KB", bytes / 1024.0);
        if (bytes < 1024L * 1024 * 1024) return String.format(Locale.US, "%.1f MB", bytes / (1024.0 * 1024));
        return String.format(Locale.US, "%.2f GB", bytes / (1024.0 * 1024 * 1024));
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
