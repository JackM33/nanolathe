#!/usr/bin/env bash
# Build the Android app (docs/ANDROID.md §4).
#
# Needs ANDROID_HOME with an SDK platform, build tools and an NDK, plus a JDK.
# Writes build/android/nanolathe-debug.apk; pass --release for an unminified
# release APK signed with the debug key, which suits sideloading.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

variant=Debug
if [ "${1:-}" = "--release" ]; then
	variant=Release
fi

: "${ANDROID_HOME:?set ANDROID_HOME to the Android SDK directory}"
export ANDROID_HOME

# ebitenmobile binds a package, and cmd/nanolathe is a command. Copy it into an
# importable package for the bound one (android/_gobind) to call. The copy
# lives only for this build.
gen=build/android/gen
rm -rf "$gen"
trap 'rm -rf "$gen"' EXIT
mkdir -p "$gen/nanolatheapp" "$gen/nanolathe"
tar -C cmd/nanolathe --exclude='*_test.go' --exclude=testdata -cf - . | tar -C "$gen/nanolatheapp" -xf -
find "$gen/nanolatheapp" -maxdepth 1 -name '*.go' -exec sed -i 's/^package main$/package nanolatheapp/' {} +
if grep -l '^package main$' "$gen"/nanolatheapp/*.go >/dev/null 2>&1; then
	echo "build.sh: a copied file kept package main" >&2
	exit 1
fi
cp android/_gobind/nanolathe.go "$gen/nanolathe/"

# Install the ebitenmobile matching the engine's Ebitengine, outside this
# module so its tool dependencies never reach go.mod.
ebiten_version=$(go list -m -f '{{.Version}}' github.com/hajimehoshi/ebiten/v2)
gobin=$root/build/android/bin
GOBIN=$gobin GOFLAGS= go install "github.com/hajimehoshi/ebiten/v2/cmd/ebitenmobile@$ebiten_version"

mkdir -p android/app/libs
"$gobin/ebitenmobile" bind \
	-target android/arm64 \
	-androidapi 24 \
	-javapkg gg.nanolathe.mobile \
	-trimpath \
	-o android/app/libs/nanolathe.aar \
	"./$gen/nanolathe"

gradle=gradle
if [ -x android/gradlew ]; then
	gradle=./gradlew
fi
(cd android && $gradle --no-daemon "assemble$variant")

lower=$(echo "$variant" | tr '[:upper:]' '[:lower:]')
apk=$(ls android/app/build/outputs/apk/"$lower"/*.apk | head -1)
cp "$apk" "build/android/nanolathe-$lower.apk"
echo "build/android/nanolathe-$lower.apk"
