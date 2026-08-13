import java.util.Properties

// Project root VERSION file → versionName / BuildConfig.VERSION / Go -X main.version
val appVersion: String = file("../../VERSION").readText().trim()

plugins {
    id("com.android.application")
}

android {
    namespace = "com.photosmove.app"
    compileSdk = 36

    defaultConfig {
        applicationId = "com.photosmove.app"
        minSdk = 26
        targetSdk = 36
        versionCode = 3   // Increment manually, +1 per release
        versionName = appVersion
        ndk { abiFilters += "arm64-v8a" }
        buildConfigField("String", "VERSION", "\"$appVersion\"")
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        buildConfig = true
    }

    packaging {
        jniLibs {
            // Critical: Go is a PIE executable and must be extracted to disk for ProcessBuilder to run it.
            // AGP defaults to useLegacyPackaging=false and overrides the manifest; without this Go cannot start.
            useLegacyPackaging = true
        }
    }

    signingConfigs {
        create("release") {
            val ksFile = rootProject.file("keystore.properties")
            if (ksFile.exists()) {
                val ks = Properties().apply { load(ksFile.inputStream()) }
                fun req(key: String) = (ks.getProperty(key) ?: error("keystore.properties missing $key"))
                    .also { require(it.isNotBlank()) { "keystore.properties $key is empty" } }
                storeFile = rootProject.file(req("storeFile"))
                storePassword = req("storePassword")
                keyAlias = req("keyAlias")
                keyPassword = req("keyPassword")
            }
        }
    }

    buildTypes {
        getByName("release") {
            isMinifyEnabled = false
            signingConfig = signingConfigs.getByName("release")
        }
    }

    sourceSets {
        getByName("main") {
            assets.srcDirs("src/main/assets", "build/generated/assets")
        }
    }
}

dependencies {
    implementation("androidx.core:core-ktx:1.13.1")
}

// === Go binary cross-compilation (replaces the go build step in build.sh) ===
val goServerDir = rootProject.projectDir.parentFile.resolve("server")
val goOutput = layout.projectDirectory.file("src/main/jniLibs/arm64-v8a/libphotosmove.so")

tasks.register<Exec>("goBuild") {
    group = "build"
    description = "Cross-compile Go binary for Android arm64"
    inputs.dir(goServerDir)
    inputs.property("appVersion", appVersion)   // VERSION changes must also trigger recompilation (-ldflags -X main.version)
    outputs.file(goOutput)
    doFirst { goOutput.asFile.parentFile.mkdirs() }
    workingDir = goServerDir
    commandLine(
        "go", "build", "-trimpath",
        "-ldflags=-s -w -X main.version=$appVersion",
        "-o", goOutput.asFile.absolutePath, "."
    )
    environment(mapOf("CGO_ENABLED" to "0", "GOOS" to "android", "GOARCH" to "arm64"))
}

// === web assets copy (web/ → build/generated/assets/web, avoiding src/main to keep git clean) ===
val webSrcDir = rootProject.projectDir.parentFile.resolve("web")
val webDstDir = layout.projectDirectory.dir("build/generated/assets/web")

tasks.register<Copy>("copyWebAssets") {
    group = "build"
    description = "Copy web/ into generated assets"
    from(webSrcDir)
    into(webDstDir)
}

// i18n translation table parity check (spec Req6 / tasks 6.1): the ui+errors key sets and
// {param} placeholders of zh.js/en.js must match, preventing missed keys from falling back to
// English on a Chinese system. Fails the build on mismatch.
tasks.register<Exec>("checkI18nParity") {
    group = "verification"
    description = "Verify zh.js/en.js translation key set + placeholder parity"
    workingDir = rootProject.projectDir.parentFile   // repo root
    commandLine("node", "web/i18n/check-parity.js")
}

// Trigger Go compilation + web copy + i18n parity check before the build
tasks.named("preBuild") {
    dependsOn("goBuild", "copyWebAssets", "checkI18nParity")
}
