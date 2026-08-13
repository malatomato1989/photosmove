package com.photosmove.app;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.BroadcastReceiver;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.provider.Settings;
import android.view.View;
import android.widget.Button;
import android.widget.ProgressBar;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

public class MainActivity extends Activity {

    private TextView statusText;
    private View statusDot;
    private TextView durationText;
    private LinearLayout urlCard;
    private TextView urlText;
    private Button copyUrlBtn;
    private LinearLayout pinCard;
    private TextView pinText;
    private Button copyPinBtn;
    private TextView wifiHint;
    private TextView wifiRequiredWarning;
    private Button permBtn;
    private View progressCard;
    private ProgressBar progressBar;
    private TextView progressText;

    private String currentPin = "";
    private String currentUrl = "";

    private static final String PREFS_NAME = "photosmove_prefs";
    private static final String KEY_ASKED_STORAGE = "asked_storage_perm";

    private Handler durationHandler;
    private long serverStartTime = 0;
    private final Runnable durationUpdater = new Runnable() {
        @Override
        public void run() {
            if (serverStartTime > 0) {
                long elapsed = (System.currentTimeMillis() - serverStartTime) / 1000;
                durationText.setText(formatDuration(elapsed));
                durationHandler.postDelayed(this, 1000);
            }
        }
    };

    private final BroadcastReceiver receiver = new BroadcastReceiver() {
        @Override
        public void onReceive(Context context, Intent intent) {
            String status = intent.getStringExtra(ServerService.EXTRA_STATUS);
            String pin = intent.getStringExtra(ServerService.EXTRA_PIN);
            String url = intent.getStringExtra(ServerService.EXTRA_URL);

            if (status != null) {
                switch (status) {
                    case "starting":
                        statusText.setText(R.string.status_starting);
                        statusDot.setBackgroundResource(R.drawable.status_dot);
                        durationText.setVisibility(View.GONE);
                        wifiRequiredWarning.setVisibility(View.GONE);
                        break;
                    case "running":
                        statusText.setText(R.string.status_running);
                        statusDot.setBackgroundResource(R.drawable.status_dot_running);
                        syncServerStartTime();
                        durationText.setVisibility(View.VISIBLE);
                        durationHandler.removeCallbacks(durationUpdater);
                        durationHandler.post(durationUpdater);
                        wifiHint.setVisibility(View.VISIBLE);
                        wifiRequiredWarning.setVisibility(View.GONE);
                        break;
                    case "wifi_required":
                        statusText.setText(R.string.status_wifi_required);
                        statusDot.setBackgroundResource(R.drawable.status_dot_error);
                        durationHandler.removeCallbacks(durationUpdater);
                        durationText.setVisibility(View.GONE);
                        urlCard.setVisibility(View.GONE);
                        wifiHint.setVisibility(View.GONE);
                        wifiRequiredWarning.setVisibility(View.VISIBLE);
                        break;
                    case "stopped":
                        statusText.setText(R.string.status_stopped);
                        statusDot.setBackgroundResource(R.drawable.status_dot);
                        durationHandler.removeCallbacks(durationUpdater);
                        durationText.setVisibility(View.GONE);
                        progressCard.setVisibility(View.GONE);
                        wifiHint.setVisibility(View.GONE);
                        wifiRequiredWarning.setVisibility(View.GONE);
                        break;
                    case "error":
                        statusText.setText(R.string.status_error);
                        statusDot.setBackgroundResource(R.drawable.status_dot_error);
                        durationHandler.removeCallbacks(durationUpdater);
                        durationText.setVisibility(View.GONE);
                        progressCard.setVisibility(View.GONE);
                        wifiHint.setVisibility(View.GONE);
                        wifiRequiredWarning.setVisibility(View.GONE);
                        break;
                }
            }

            if (pin != null && !pin.isEmpty()) {
                currentPin = pin;
                pinCard.setVisibility(View.VISIBLE);
                pinText.setText(pin);
            }

            if (url != null && !url.isEmpty()) {
                currentUrl = url;
                urlCard.setVisibility(View.VISIBLE);
                urlText.setText(url);
            }

            String progress = intent.getStringExtra(ServerService.EXTRA_PROGRESS);
            if (progress != null && !progress.isEmpty()) {
                progressCard.setVisibility(View.VISIBLE);
                progressText.setText(progress);
                // Extract percentage from progress string (format: "45% · 2.3GB/5.1GB · photo.jpg")
                int pctEnd = progress.indexOf('%');
                if (pctEnd > 0) {
                    try {
                        int pct = Integer.parseInt(progress.substring(0, pctEnd).trim());
                        progressBar.setProgress(pct);
                    } catch (NumberFormatException ignored) {}
                }
            }

            String transferState = intent.getStringExtra(ServerService.EXTRA_TRANSFER_STATE);
            if (transferState != null) {
                switch (transferState) {
                    case ServerService.STATE_DONE:
                        progressBar.setProgress(100);
                        progressText.setText(R.string.transfer_done);
                        break;
                    case ServerService.STATE_CANCELLED:
                        progressText.setText(R.string.transfer_cancelled);
                        break;
                    case ServerService.STATE_PAUSED:
                        progressText.setText(R.string.transfer_paused);
                        break;
                }
            }
        }
    };

    @Override
    protected void attachBaseContext(Context newBase) {
        super.attachBaseContext(LocaleHelper.wrap(newBase));
    }

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_main);

        statusText = findViewById(R.id.status_text);
        statusDot = findViewById(R.id.status_dot);
        durationText = findViewById(R.id.duration_text);
        urlCard = findViewById(R.id.url_card);
        urlText = findViewById(R.id.url_text);
        copyUrlBtn = findViewById(R.id.copy_url_btn);
        pinCard = findViewById(R.id.pin_card);
        pinText = findViewById(R.id.pin_text);
        copyPinBtn = findViewById(R.id.copy_pin_btn);
        wifiHint = findViewById(R.id.wifi_hint);
        wifiRequiredWarning = findViewById(R.id.wifi_required_warning);
        permBtn = findViewById(R.id.permission_btn);
        progressCard = findViewById(R.id.progress_card);
        progressBar = findViewById(R.id.progress_bar);
        progressText = findViewById(R.id.progress_text);

        durationHandler = new Handler(Looper.getMainLooper());

        // Show version from ServerService
        TextView versionText = findViewById(R.id.version_text);
        versionText.setText("v" + ServerService.VERSION);

        // Language switcher (group 5.2): shows the current language; tapping opens a picker list
        TextView langBtn = findViewById(R.id.lang_btn);
        updateLangBtn(langBtn);
        langBtn.setOnClickListener(v -> showLangPicker());

        copyUrlBtn.setOnClickListener(v -> {
            copyToClipboard("URL", currentUrl);
            showCopyDone(copyUrlBtn);
        });
        copyPinBtn.setOnClickListener(v -> {
            copyToClipboard("PIN", currentPin);
            showCopyDone(copyPinBtn);
        });

        permBtn.setOnClickListener(v -> requestStoragePermission());

        // Privacy policy entry: tap to open in the system browser (Play compliance: accessible in-app)
        findViewById(R.id.privacy_link).setOnClickListener(v -> {
            try {
                startActivity(new Intent(Intent.ACTION_VIEW,
                        Uri.parse(getString(R.string.privacy_policy_url))));
            } catch (Exception e) {
                Toast.makeText(this, R.string.no_browser, Toast.LENGTH_SHORT).show();
            }
        });

        IntentFilter filter = new IntentFilter(ServerService.ACTION_UPDATE);
        if (Build.VERSION.SDK_INT >= 33) {
            registerReceiver(receiver, filter, Context.RECEIVER_NOT_EXPORTED);
        } else {
            registerReceiver(receiver, filter);
        }

        if (hasStoragePermission()) {
            startServer();
            // Restore last state if service is already running
            restoreState();
        } else {
            statusText.setText(R.string.status_idle);
            permBtn.setVisibility(View.VISIBLE);
        }
    }

    @Override
    protected void onResume() {
        super.onResume();
        // Returning to the foreground must re-sync the UI from the running Service. A recreated
        // Activity that skipped the normal start path (e.g. process killed while the foreground
        // Service survived, or after a permission revoke/regrant cycle) would otherwise stay
        // stuck on "Starting service…" until a full app restart.
        if (hasStoragePermission()) {
            restoreState();
        }
    }

    @Override
    protected void onDestroy() {
        super.onDestroy();
        durationHandler.removeCallbacks(durationUpdater);
        try {
            unregisterReceiver(receiver);
        } catch (Exception ignored) {}
    }

    private void restoreState() {
        String status = ServerService.getLastStatus();
        String pin = ServerService.getLastPin();
        String url = ServerService.getLastUrl();

        if (!url.isEmpty()) {
            currentUrl = url;
            urlCard.setVisibility(View.VISIBLE);
            urlText.setText(url);
        }
        if (!pin.isEmpty()) {
            currentPin = pin;
            pinCard.setVisibility(View.VISIBLE);
            pinText.setText(pin);
        }
        switch (status) {
            case "running":
                statusText.setText(R.string.status_running);
                statusDot.setBackgroundResource(R.drawable.status_dot_running);
                durationText.setVisibility(View.VISIBLE);
                syncServerStartTime();
                durationHandler.removeCallbacks(durationUpdater);
                durationHandler.post(durationUpdater);
                wifiHint.setVisibility(View.VISIBLE);
                wifiRequiredWarning.setVisibility(View.GONE);
                break;
            case "wifi_required":
                statusText.setText(R.string.status_wifi_required);
                statusDot.setBackgroundResource(R.drawable.status_dot_error);
                urlCard.setVisibility(View.GONE);
                wifiHint.setVisibility(View.GONE);
                wifiRequiredWarning.setVisibility(View.VISIBLE);
                break;
            case "stopped":
                statusText.setText(R.string.status_stopped);
                statusDot.setBackgroundResource(R.drawable.status_dot);
                break;
            case "error":
                statusText.setText(R.string.status_error);
                statusDot.setBackgroundResource(R.drawable.status_dot_error);
                break;
            default:
                break;
        }
    }

    private void showCopyDone(Button btn) {
        CharSequence original = btn.getText();
        btn.setText(R.string.btn_copy_done);
        btn.setEnabled(false);
        durationHandler.postDelayed(() -> {
            btn.setText(original);
            btn.setEnabled(true);
        }, 1500);
    }

    // Derive the uptime origin from the Service's real start epoch so the counter survives
    // Activity recreation (the system can reclaim the task while the foreground Service runs) —
    // otherwise returning to the foreground restarts the uptime from 0.
    private void syncServerStartTime() {
        long s = ServerService.getLastStartEpoch();
        serverStartTime = (s > 0) ? s : System.currentTimeMillis();
    }

    private String formatDuration(long seconds) {
        if (seconds < 60) return getString(R.string.duration_seconds, seconds);
        long min = seconds / 60;
        long sec = seconds % 60;
        if (min < 60) return String.format("%d:%02d", min, sec);
        long hr = min / 60;
        min = min % 60;
        return String.format("%d:%02d:%02d", hr, min, sec);
    }

    private void updateLangBtn(TextView btn) {
        String code = LocaleHelper.current(this);
        btn.setText("zh".equals(code) ? getText(R.string.lang_zh) : getText(R.string.lang_en));
    }

    private void showLangPicker() {
        boolean followingSystem = LocaleHelper.isFollowingSystem(this);
        String current = LocaleHelper.current(this);

        // List = supported languages + "follow system" (clears per-app, follows system language).
        java.util.List<String> codes = new ArrayList<>(LocaleHelper.LANGUAGES.keySet());
        java.util.List<String> labels = new ArrayList<>(LocaleHelper.LANGUAGES.values());
        codes.add("system");
        labels.add(getString(R.string.lang_system));

        int checked = 0;
        for (int i = 0; i < codes.size(); i++) {
            String c = codes.get(i);
            boolean sel = "system".equals(c) ? followingSystem : (!followingSystem && c.equals(current));
            if (sel) { checked = i; break; }
        }
        String[] labelArr = labels.toArray(new String[0]);
        String[] codeArr = codes.toArray(new String[0]);
        new AlertDialog.Builder(this)
                .setTitle(R.string.lang_selector_title)
                .setSingleChoiceItems(labelArr, checked, (d, which) -> {
                    String code = codeArr[which];
                    if ("system".equals(code)) {
                        if (!followingSystem) {
                            LocaleHelper.clear(this);
                            ServerService.refreshNotificationLocale(this);
                            if (Build.VERSION.SDK_INT < 33) recreate();
                        }
                    } else {
                        if (!code.equals(LocaleHelper.getManual(this))) {
                            LocaleHelper.apply(this, code);
                            ServerService.refreshNotificationLocale(this);
                            if (Build.VERSION.SDK_INT < 33) recreate();
                        }
                    }
                    d.dismiss();
                })
                .show();
    }

    private boolean hasStoragePermission() {
        if (Build.VERSION.SDK_INT >= 33) {
            return checkSelfPermission(Manifest.permission.READ_MEDIA_IMAGES) == PackageManager.PERMISSION_GRANTED
                    && checkSelfPermission(Manifest.permission.READ_MEDIA_VIDEO) == PackageManager.PERMISSION_GRANTED;
        }
        return checkSelfPermission(Manifest.permission.READ_EXTERNAL_STORAGE)
                == PackageManager.PERMISSION_GRANTED;
    }

    private void requestStoragePermission() {
        List<String> perms = new ArrayList<>();
        if (Build.VERSION.SDK_INT >= 33) {
            perms.add(Manifest.permission.READ_MEDIA_IMAGES);
            perms.add(Manifest.permission.READ_MEDIA_VIDEO);
        } else {
            perms.add(Manifest.permission.READ_EXTERNAL_STORAGE);
        }
        if (Build.VERSION.SDK_INT >= 33) {
            perms.add(Manifest.permission.POST_NOTIFICATIONS);
        }

        SharedPreferences prefs = getSharedPreferences(PREFS_NAME, MODE_PRIVATE);
        boolean askedBefore = prefs.getBoolean(KEY_ASKED_STORAGE, false);
        // After the first ask, if the system will no longer show the permission dialog (the user
        // picked "Don't ask again", or — on some ROMs — the permission was revoked from system
        // settings), requestPermissions() denies silently with no UI, so the button looks dead.
        // Detect that and route the user to the system permission settings instead.
        if (askedBefore && !shouldShowStorageRationale()) {
            showOpenPermissionSettingsDialog();
            return;
        }
        prefs.edit().putBoolean(KEY_ASKED_STORAGE, true).apply();
        requestPermissions(perms.toArray(new String[0]), 101);
    }

    // Whether the system would still show a rationale / permission dialog for any storage perm.
    private boolean shouldShowStorageRationale() {
        if (Build.VERSION.SDK_INT >= 33) {
            return shouldShowRequestPermissionRationale(Manifest.permission.READ_MEDIA_IMAGES)
                    || shouldShowRequestPermissionRationale(Manifest.permission.READ_MEDIA_VIDEO);
        }
        return shouldShowRequestPermissionRationale(Manifest.permission.READ_EXTERNAL_STORAGE);
    }

    // Fallback when the permission dialog can no longer be shown automatically: explain and open
    // the system app-settings page so the user can grant "Photos and videos" manually.
    private void showOpenPermissionSettingsDialog() {
        new AlertDialog.Builder(this)
                .setTitle(R.string.app_name)
                .setMessage(R.string.perm_required_message)
                .setPositiveButton(R.string.perm_settings_open, (d, w) -> {
                    try {
                        Intent intent = new Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS);
                        intent.setData(Uri.parse("package:" + getPackageName()));
                        startActivity(intent);
                    } catch (Exception e) {
                        Toast.makeText(this, R.string.no_browser, Toast.LENGTH_SHORT).show();
                    }
                })
                .setNegativeButton(android.R.string.cancel, null)
                .show();
    }

    @Override
    public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);
        if (requestCode == 101 && grantResults.length > 0) {
            boolean allGranted = true;
            for (int r : grantResults) {
                if (r != PackageManager.PERMISSION_GRANTED) {
                    allGranted = false;
                    break;
                }
            }
            if (allGranted || hasStoragePermission()) {
                startServer();
                // Re-granting storage permission may hit a Service that kept running while the
                // permission was revoked: startServer → onStartCommand is a no-op (guarded by
                // serverStarted), so it never re-broadcasts URL/PIN. Pull the current state from
                // the Service so the UI does not stay on "Starting service…".
                restoreState();
            }
        }
    }

    private void startServer() {
        permBtn.setVisibility(View.GONE);
        statusText.setText(R.string.status_starting);

        Intent intent = new Intent(this, ServerService.class);
        if (Build.VERSION.SDK_INT >= 26) {
            startForegroundService(intent);
        } else {
            startService(intent);
        }
    }

    private void copyToClipboard(String label, String text) {
        ClipboardManager cm = (ClipboardManager) getSystemService(Context.CLIPBOARD_SERVICE);
        cm.setPrimaryClip(ClipData.newPlainText(label, text));
    }
}
