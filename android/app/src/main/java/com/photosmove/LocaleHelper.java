package com.photosmove;

import android.content.Context;
import android.content.SharedPreferences;
import android.content.res.Configuration;
import android.os.Build;
import android.os.LocaleList;
import java.util.LinkedHashMap;
import java.util.Locale;
import java.util.Map;

/**
 * i18n locale 管理 (design D2). 纯原生方案, 不引入 AppCompat.
 *
 * 优先级: 用户手动选择 > 系统语言 > 英文兜底.
 *  - API 33+ : android.app.LocaleManager (系统级 per-app 语言, 自动接入系统设置)
 *  - API 26-32: attachBaseContext 用 Configuration.setLocale + createConfigurationContext wrap
 *
 * 加新语言 = LANGUAGES 注册表加一项 + 建 values-xx/strings.xml (spec Req4 声明性登记, 零逻辑改动).
 */
public final class LocaleHelper {
    private static final String PREFS = "photosmove_locale";
    private static final String KEY = "locale";

    /** 支持语言注册表: code → 显示名. 新增语言在此加一项即可. */
    public static final Map<String, String> LANGUAGES = new LinkedHashMap<>();
    static {
        LANGUAGES.put("zh", "中文");
        LANGUAGES.put("en", "English");
    }

    private LocaleHelper() {}

    private static SharedPreferences prefs(Context ctx) {
        return ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    /** 用户手动选择的 code; null = 未选(跟随系统). */
    public static String getManual(Context ctx) {
        String v = prefs(ctx).getString(KEY, null);
        return (v != null && LANGUAGES.containsKey(v)) ? v : null;
    }

    /**
     * 当前生效 code: 手动选择 > 系统语言匹配 > 英文兜底.
     * API 33+: 优先读 LocaleManager — 用户可能通过系统设置改了 app 语言, 此时
     * prefs (仅在 apply() 内写) 尚未同步, 直接读 prefs 会让语言按钮错标.
     * LocaleManager 为空 (用户在系统设置清了 per-app 语言 = 跟随系统) 时, 跳过陈旧
     * prefs (apply 只写不清), 直接匹配系统语言, 避免按钮按旧 prefs 错标.
     */
    public static String current(Context ctx) {
        if (Build.VERSION.SDK_INT >= 33) {
            try {
                android.app.LocaleManager lm =
                        (android.app.LocaleManager) ctx.getSystemService(Context.LOCALE_SERVICE);
                if (lm != null) {
                    LocaleList ll = lm.getApplicationLocales();
                    if (ll != null && !ll.isEmpty()) {
                        String lang = ll.get(0).getLanguage();
                        for (String code : LANGUAGES.keySet()) {
                            if (code.equalsIgnoreCase(lang)) return code;
                        }
                    } else {
                        // LM 可读且为空: 跟随系统, 跳过陈旧 prefs.
                        return systemDefault();
                    }
                }
            } catch (Exception ignored) {}
        }
        String manual = getManual(ctx);
        if (manual != null) return manual;
        return systemDefault();
    }

    /** 是否处于「跟随系统」状态 (无 per-app / 无手动选择). picker 据此标「跟随系统」项. */
    public static boolean isFollowingSystem(Context ctx) {
        if (Build.VERSION.SDK_INT >= 33) {
            try {
                android.app.LocaleManager lm =
                        (android.app.LocaleManager) ctx.getSystemService(Context.LOCALE_SERVICE);
                if (lm != null) {
                    LocaleList ll = lm.getApplicationLocales();
                    return ll == null || ll.isEmpty();
                }
            } catch (Exception ignored) {}
        }
        return getManual(ctx) == null;
    }

    private static String systemDefault() {
        String sys = Locale.getDefault().getLanguage();
        for (String code : LANGUAGES.keySet()) {
            if (code.equalsIgnoreCase(sys)) return code;
        }
        return "en";
    }

    /**
     * attachBaseContext 调用. 仅当「用户手动选了语言」且「API 26-32」时才 wrap context;
     * 其余情况(跟随系统 / API33+ 由 LocaleManager 管)直接返回 base, 让 Android 资源系统自动选.
     */
    public static Context wrap(Context base) {
        String manual = getManual(base);
        if (manual == null) return base;
        if (Build.VERSION.SDK_INT >= 33) return base;
        Locale loc = localeOf(manual);
        Locale.setDefault(loc);
        Configuration cfg = new Configuration(base.getResources().getConfiguration());
        cfg.setLocale(loc);
        cfg.setLayoutDirection(loc);
        return base.createConfigurationContext(cfg);
    }

    /**
     * 切换语言: 写 SharedPreferences 持久化 + (API 33+) 设 LocaleManager.
     * 调用方随后调 Activity.recreate() 使新 locale 生效 (API 33+ 由 LocaleManager 触发系统重建,
     * 无需手动 recreate).
     */
    public static void apply(Context ctx, String code) {
        prefs(ctx).edit().putString(KEY, code).apply();
        if (Build.VERSION.SDK_INT >= 33) {
            try {
                android.app.LocaleManager lm =
                        (android.app.LocaleManager) ctx.getSystemService(Context.LOCALE_SERVICE);
                if (lm != null) {
                    lm.setApplicationLocales(new LocaleList(localeOf(code)));
                }
            } catch (Exception ignored) {}
        }
    }

    /**
     * 清除手动选择, 回到跟随系统语言: 删 prefs + (API 33+) 清 per-app locale.
     * 调用方随后调 Activity.recreate() (API 26-32 需手动 recreate 使 attachBaseContext
     * 走系统语言; API 33+ 由 LocaleManager 触发系统重建).
     */
    public static void clear(Context ctx) {
        prefs(ctx).edit().remove(KEY).apply();
        if (Build.VERSION.SDK_INT >= 33) {
            try {
                android.app.LocaleManager lm =
                        (android.app.LocaleManager) ctx.getSystemService(Context.LOCALE_SERVICE);
                if (lm != null) {
                    lm.setApplicationLocales(LocaleList.getEmptyLocaleList());
                }
            } catch (Exception ignored) {}
        }
    }

    public static Locale localeOf(String code) {
        // zh 用 SIMPLIFIED_CHINESE 精确匹配 values-zh-rCN/values-zh; 其余 code 直接构造,
        // 满足 spec Req4「加新语言零逻辑改动」(原硬编码 zh/en 二选一会把第三语言回落 English).
        if ("zh".equals(code)) return Locale.SIMPLIFIED_CHINESE;
        return new Locale(code);
    }

    /**
     * 运行中切语言时, ServerService 用此获取「当前应生效 locale」的 context (而非冻结的 this).
     * 与 wrap() 区别: wrap 用于 attachBaseContext (进程启动, base 已是系统 locale, manual==null
     * 返回 base 即可); resolveLocalizedContext 用于运行中 refresh, base (Activity) 可能仍是旧
     * manual 的 locale, 故 manual==null (跟随系统) 时也需显式 createConfigurationContext(系统 locale),
     * 否则 clear() 路径会拿到旧 locale 的 Activity context 重建出旧语言通知 (review 第三轮 finding).
     * API 33+ 由 LocaleManager app 级接管, 直接返回 base.
     */
    public static Context resolveLocalizedContext(Context base) {
        if (Build.VERSION.SDK_INT >= 33) return base;
        String manual = getManual(base);
        Locale loc = (manual != null) ? localeOf(manual) : Locale.getDefault();
        Locale.setDefault(loc);
        Configuration cfg = new Configuration(base.getResources().getConfiguration());
        cfg.setLocale(loc);
        cfg.setLayoutDirection(loc);
        return base.createConfigurationContext(cfg);
    }
}
