package com.photosmove.app;

import android.content.Context;
import android.content.SharedPreferences;
import android.content.res.Configuration;
import android.os.Build;
import android.os.LocaleList;
import java.util.LinkedHashMap;
import java.util.Locale;
import java.util.Map;

/**
 * i18n locale management (design D2). Pure native approach, no AppCompat.
 *
 * Priority: user manual selection > system language > English fallback.
 *  - API 33+ : android.app.LocaleManager (system-level per-app language, auto-integrated with system settings)
 *  - API 26-32: attachBaseContext wraps via Configuration.setLocale + createConfigurationContext
 *
 * Adding a new language = add one entry to the LANGUAGES registry + create values-xx/strings.xml
 * (spec Req4 declarative registration, zero logic changes).
 */
public final class LocaleHelper {
    private static final String PREFS = "photosmove_locale";
    private static final String KEY = "locale";

    /** Supported language registry: code → display name. Add one entry here for a new language. */
    public static final Map<String, String> LANGUAGES = new LinkedHashMap<>();
    static {
        LANGUAGES.put("zh", "中文");
        LANGUAGES.put("en", "English");
    }

    private LocaleHelper() {}

    private static SharedPreferences prefs(Context ctx) {
        return ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    /** User's manually selected code; null = not selected (follow system). */
    public static String getManual(Context ctx) {
        String v = prefs(ctx).getString(KEY, null);
        return (v != null && LANGUAGES.containsKey(v)) ? v : null;
    }

    /**
     * Currently effective code: manual selection > system language match > English fallback.
     * API 33+: read LocaleManager first — the user may have changed the app language via
     * system settings, in which case prefs (written only inside apply()) is not yet synced,
     * and reading prefs directly would mislabel the language button.
     * When LocaleManager is empty (user cleared per-app language in system settings = follow
     * system), skip stale prefs (apply only writes, never clears) and match the system
     * language directly, so the button is not mislabeled by old prefs.
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
                        // LM readable and empty: follow system, skip stale prefs.
                        return systemDefault();
                    }
                }
            } catch (Exception ignored) {}
        }
        String manual = getManual(ctx);
        if (manual != null) return manual;
        return systemDefault();
    }

    /** Whether in "follow system" state (no per-app / no manual selection). The picker marks the "follow system" item accordingly. */
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
     * Called from attachBaseContext. Only wraps the context when "the user manually chose a
     * language" AND "API 26-32"; otherwise (follow system / API 33+ managed by LocaleManager)
     * returns base directly and lets the Android resource system pick automatically.
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
     * Switch language: persist to SharedPreferences + (API 33+) set LocaleManager.
     * The caller then calls Activity.recreate() to apply the new locale (on API 33+
     * LocaleManager triggers a system recreation, so no manual recreate is needed).
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
     * Clear the manual selection and go back to following the system language: remove prefs +
     * (API 33+) clear per-app locale.
     * The caller then calls Activity.recreate() (API 26-32 needs a manual recreate so
     * attachBaseContext follows the system language; API 33+ is handled by LocaleManager).
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
        // zh uses SIMPLIFIED_CHINESE to match values-zh-rCN/values-zh exactly; other codes are
        // constructed directly, satisfying spec Req4 "adding a new language requires zero logic
        // changes" (the original hardcoded zh/en switch would fall a third language back to English).
        if ("zh".equals(code)) return Locale.SIMPLIFIED_CHINESE;
        return new Locale(code);
    }

    /**
     * When switching language at runtime, ServerService uses this to get a context with the
     * "currently effective locale" (instead of the frozen `this`).
     * Difference from wrap(): wrap is for attachBaseContext (process start, base already carries
     * the system locale, returning base when manual==null is fine); resolveLocalizedContext is
     * for runtime refresh, where base (Activity) may still carry the old manual locale, so even
     * when manual==null (follow system) an explicit createConfigurationContext(system locale)
     * is required — otherwise the clear() path would rebuild notifications in the old language
     * from a stale-locale Activity context (review round 3 finding).
     * API 33+ is taken over app-wide by LocaleManager, so base is returned directly.
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
