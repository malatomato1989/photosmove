package com.photosmove;

import android.app.Application;
import android.content.Context;

/**
 * Custom Application: overrides attachBaseContext to call LocaleHelper.wrap, so
 * components using the Application context (Service / Notification, etc.) also
 * follow a manually chosen language on API 26-32
 * (design D7: notification body follows language).
 *
 * MainActivity already wraps its Activity context separately; but ServerService is a
 * Service, and its getString(R.string.notif_*) resolves through the Application's
 * Configuration. If Application were not wrapped, manually choosing a language on
 * API 26-32 devices would show the Activity in English while the notification bar
 * stays in Chinese.
 *
 * API 33+: LocaleHelper.wrap returns base directly (android.app.LocaleManager manages
 * the app-level locale and covers all components automatically), so this is a no-op.
 */
public final class App extends Application {
    @Override
    protected void attachBaseContext(Context base) {
        super.attachBaseContext(LocaleHelper.wrap(base));
    }
}
