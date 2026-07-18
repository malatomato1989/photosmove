package com.photosmove;

import android.Manifest;
import android.app.Activity;
import android.content.BroadcastReceiver;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.pm.PackageManager;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.View;
import android.widget.Button;
import android.widget.ProgressBar;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;

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
                        serverStartTime = System.currentTimeMillis();
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

        copyUrlBtn.setOnClickListener(v -> {
            copyToClipboard("URL", currentUrl);
            showCopyDone(copyUrlBtn);
        });
        copyPinBtn.setOnClickListener(v -> {
            copyToClipboard("PIN", currentPin);
            showCopyDone(copyPinBtn);
        });

        permBtn.setOnClickListener(v -> requestStoragePermission());

        IntentFilter filter = new IntentFilter(ServerService.ACTION_UPDATE);
        if (Build.VERSION.SDK_INT >= 33) {
            registerReceiver(receiver, filter, Context.RECEIVER_EXPORTED);
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
                serverStartTime = System.currentTimeMillis();
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

    private String formatDuration(long seconds) {
        if (seconds < 60) return getString(R.string.duration_seconds, seconds);
        long min = seconds / 60;
        long sec = seconds % 60;
        if (min < 60) return String.format("%d:%02d", min, sec);
        long hr = min / 60;
        min = min % 60;
        return String.format("%d:%02d:%02d", hr, min, sec);
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
        requestPermissions(perms.toArray(new String[0]), 101);
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
