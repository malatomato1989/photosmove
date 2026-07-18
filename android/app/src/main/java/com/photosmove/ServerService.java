package com.photosmove;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
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
    public static final String ACTION_UPDATE = "com.photosmove.UPDATE";
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

    public static String getLastStatus() { return lastStatus; }
    public static String getLastPin() { return lastPin; }
    public static String getLastUrl() { return lastUrl; }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        createNotificationChannel();
        Intent notifIntent = new Intent(this, MainActivity.class);
        PendingIntent pi = PendingIntent.getActivity(this, 0, notifIntent,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
        Notification notification = new Notification.Builder(this, CHANNEL_ID)
                .setContentTitle(getString(R.string.app_name))
                .setContentText(getString(R.string.notif_service_running))
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
                // SCREEN_DIM_WAKE_LOCK: 屏幕变暗但保持亮起, 防止 HyperOS/MIUI
                // 在屏幕灭时把 FGS 当 idle 杀掉 (PARTIAL_WAKE_LOCK 只保 CPU,
                // 屏幕灭后 60s SmartPower 仍会 cancel FGS notification + stop service).
                // deprecated 但仍可用, 是 HyperOS 后台限制的唯一规避方式.
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
                    CHANNEL_ID, getString(R.string.notif_channel_name), NotificationManager.IMPORTANCE_HIGH);
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
        // 优先 ConnectivityManager.getLinkProperties (现代 API, Android 6+).
        // WifiManager.getConnectionInfo().getIpAddress() 在 Android 12+ 不可靠 (返回 0).
        try {
            ConnectivityManager cm = (ConnectivityManager)
                    getApplicationContext().getSystemService(CONNECTIVITY_SERVICE);
            if (cm != null) {
                Network active = cm.getActiveNetwork();
                if (active != null) {
                    String ip = extractIpv4(cm.getLinkProperties(active));
                    if (ip != null) return ip;
                }
            }
        } catch (Exception e) {
            Log.e(TAG, "getWifiIP (ConnectivityManager) failed", e);
        }
        // fallback: WifiManager.getIpAddress (旧设备/极端情况)
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

    // 从 LinkProperties 提取首个 IPv4 非 loopback 地址, 无则 null.
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

    // 注册 NetworkCallback 监听 WiFi 连接/断开, 实现 URL 自动更新 (无需重启 app).
    // 回调在系统 binder 线程触发, broadcast/updateNotification 线程安全.
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
                    updateNotification(getString(R.string.notif_wifi_required));
                }
            };

            cm.registerNetworkCallback(req, networkCallback);

            // 初始检测: 若当前无活动 WiFi (或无 IPv4), 立即提示 wifi_required.
            // (若 WiFi 已连, onLinkPropertiesChanged 会很快触发并广播真实 URL.)
            Network active = cm.getActiveNetwork();
            boolean hasWifi = false;
            if (active != null) {
                NetworkCapabilities caps = cm.getNetworkCapabilities(active);
                if (caps != null && caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) {
                    if (extractIpv4(cm.getLinkProperties(active)) != null) {
                        hasWifi = true;
                    }
                }
            }
            if (!hasWifi) {
                Log.i(TAG, "No active WiFi at startup → wifi_required");
                broadcast("wifi_required", null, null);
                updateNotification(getString(R.string.notif_wifi_required));
            } else {
                wifiConnected = true;
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
                .setContentTitle(getString(R.string.app_name))
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

            // getWifiIP 一次取值, 失败则不传 -lan-ip —— Go 端 getLANIP 用 udp 探测兜底
            // (net.Dial 查路由表, 比 WifiManager.getIpAddress 鲁棒, 不依赖 WiFi 就绪状态).
            // 不再阻塞启动重试 (之前 10s 循环导致 app 启动慢).
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

            // 注册 NetworkCallback: WiFi 断开→提示, WiFi 连上→自动更新 URL (无需重启 app).
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
                            updateNotification(getString(R.string.notif_transferring, pct, formatSize(sent), formatSize(total)));
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
                    updateNotification(getString(R.string.notif_done, sizeStr), false);
                    broadcast(null, null, null, null, STATE_DONE);
                    continue;
                }

                if (line.contains("CANCELLED:")) {
                    updateNotification(getString(R.string.notif_cancelled), false);
                    broadcast(null, null, null, null, STATE_CANCELLED);
                    continue;
                }

                if (line.contains("PAUSED:")) {
                    updateNotification(getString(R.string.notif_paused), false);
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
                    // Go 在 WiFi 未连时打印 0.0.0.0 —— 此时不广播 running URL,
                    // 由 NetworkCallback 负责 wifi_required 提示 / 真实 URL 更新.
                    if (!detectedUrl.contains("0.0.0.0")) {
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
