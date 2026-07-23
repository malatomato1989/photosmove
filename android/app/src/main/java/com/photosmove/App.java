package com.photosmove;

import android.app.Application;
import android.content.Context;

/**
 * 自定义 Application: 重写 attachBaseContext 调 LocaleHelper.wrap, 使 Service /
 * Notification 等使用 Application context 的组件在 API 26-32 手动选语言时也跟随
 * (design D7: 通知正文跟随语言).
 *
 * MainActivity 已单独 wrap Activity context; 但 ServerService 是 Service, 其
 * getString(R.string.notif_*) 走 Application 的 Configuration. 若 Application 不
 * wrap, API 26-32 设备手动选语言后, Activity 英文而通知栏仍中文.
 *
 * API 33+ : LocaleHelper.wrap 直接返回 base (由 android.app.LocaleManager 管 app 级
 * locale, 自动覆盖所有组件), 故此处 no-op.
 */
public final class App extends Application {
    @Override
    protected void attachBaseContext(Context base) {
        super.attachBaseContext(LocaleHelper.wrap(base));
    }
}
