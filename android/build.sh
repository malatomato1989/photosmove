#!/bin/bash
# photosmove Android build thin wrapper (delegates to Gradle)
#
#   bash build.sh          → ./gradlew :app:bundleRelease, produces release AAB
#                            app/build/outputs/bundle/release/app-release.aab
#   bash build.sh -i       → ./gradlew :app:installDebug, dev install to a real device
#
# Note: since v0.13 the build system has moved to Gradle (AGP 8.7 + Gradle 8.9);
#       the old hand-written aapt2/d8 flow is retired. The Go binary is cross-compiled
#       by the goBuild task in app/build.gradle.kts.
set -e
DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

case "$1" in
    --install|-i)
        echo "=== ./gradlew :app:installDebug (dev install) ==="
        ./gradlew :app:installDebug
        echo ""
        echo "✅ Debug APK installed to device"
        ;;
    *)
        echo "=== ./gradlew :app:bundleRelease (produces AAB) ==="
        ./gradlew :app:bundleRelease
        AAB="app/build/outputs/bundle/release/app-release.aab"
        if [ -f "$AAB" ]; then
            echo ""
            echo "✅ AAB: $AAB ($(ls -lh "$AAB" | awk '{print $5}'))"
        fi
        ;;
esac
