import java.util.Properties

// 项目根 VERSION 文件 → versionName / BuildConfig.VERSION / Go -X main.version
val appVersion: String = file("../../VERSION").readText().trim()

plugins {
    id("com.android.application")
}

android {
    namespace = "com.photosmove"
    compileSdk = 36

    defaultConfig {
        applicationId = "com.photosmove"
        minSdk = 26
        targetSdk = 36
        versionCode = 2   // 手动递增，每次发版 +1
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
            // 关键：Go 是 PIE 可执行文件，必须 extract 到磁盘才能被 ProcessBuilder 执行
            // AGP 默认 useLegacyPackaging=false 且覆盖 manifest，不设则 Go 无法启动
            useLegacyPackaging = true
        }
    }

    signingConfigs {
        create("release") {
            val ksFile = rootProject.file("keystore.properties")
            if (ksFile.exists()) {
                val ks = Properties().apply { load(ksFile.inputStream()) }
                fun req(key: String) = (ks.getProperty(key) ?: error("keystore.properties 缺少 $key"))
                    .also { require(it.isNotBlank()) { "keystore.properties 的 $key 为空" } }
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

// === Go 二进制交叉编译（替代 build.sh 的 go build 步骤）===
val goServerDir = rootProject.projectDir.parentFile.resolve("server")
val goOutput = layout.projectDirectory.file("src/main/jniLibs/arm64-v8a/libphotosmove.so")

tasks.register<Exec>("goBuild") {
    group = "build"
    description = "Cross-compile Go binary for Android arm64"
    inputs.dir(goServerDir)
    inputs.property("appVersion", appVersion)   // VERSION 变化也要触发重编译（-ldflags -X main.version）
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

// === web 资产 copy（web/ → build/generated/assets/web，避开 src/main 防 git 污染）===
val webSrcDir = rootProject.projectDir.parentFile.resolve("web")
val webDstDir = layout.projectDirectory.dir("build/generated/assets/web")

tasks.register<Copy>("copyWebAssets") {
    group = "build"
    description = "Copy web/ into generated assets"
    from(webSrcDir)
    into(webDstDir)
}

// i18n 翻译表一致性校验 (spec Req6 / tasks 6.1): zh.js/en.js 的 ui+errors key 集合 +
// {param} 占位符必须一致, 防漏翻 key 导致中文系统回退显示英文. 失败则中断构建.
tasks.register<Exec>("checkI18nParity") {
    group = "verification"
    description = "Verify zh.js/en.js translation key set + placeholder parity"
    workingDir = rootProject.projectDir.parentFile   // repo root
    commandLine("node", "web/i18n/check-parity.js")
}

// 构建前触发 Go 编译 + web 拷贝 + i18n 一致性校验
tasks.named("preBuild") {
    dependsOn("goBuild", "copyWebAssets", "checkI18nParity")
}
