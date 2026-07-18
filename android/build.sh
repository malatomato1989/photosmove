#!/bin/bash
# photosmove Android 构建薄封装（委托 Gradle）
#
#   bash build.sh          → ./gradlew :app:bundleRelease，产出 release AAB
#                            app/build/outputs/bundle/release/app-release.aab
#   bash build.sh -i       → ./gradlew :app:installDebug，开发装机到真机
#
# 说明：自 v0.13 起构建体系迁至 Gradle（AGP 8.7 + Gradle 8.9），
#       旧的 aapt2/d8 手写流程已退役。Go 二进制由 app/build.gradle.kts 的 goBuild task 交叉编译。
set -e
DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

case "$1" in
    --install|-i)
        echo "=== ./gradlew :app:installDebug（开发装机）==="
        ./gradlew :app:installDebug
        echo ""
        echo "✅ Debug APK 已安装到真机"
        ;;
    *)
        echo "=== ./gradlew :app:bundleRelease（产出 AAB）==="
        ./gradlew :app:bundleRelease
        AAB="app/build/outputs/bundle/release/app-release.aab"
        if [ -f "$AAB" ]; then
            echo ""
            echo "✅ AAB: $AAB ($(ls -lh "$AAB" | awk '{print $5}'))"
        fi
        ;;
esac
