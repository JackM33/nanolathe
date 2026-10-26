#!/usr/bin/env bash
# Build the Android app (docs/ANDROID.md §4).
#
# Needs ANDROID_HOME with an SDK platform, build tools and an NDK, plus a JDK.
# Writes build/android/nanolathe-debug.apk for 64-bit ARM phones.
#   --release   an unminified release APK signed with the debug key, which
#               suits sideloading
#   --emulator  an x86_64 APK for Android emulators on a PC
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

variant=Debug
target=android/arm64
abi=arm64-v8a
suffix=
for arg in "$@"; do
	case "$arg" in
	--release) variant=Release ;;
	--emulator) target=android/amd64 abi=x86_64 suffix=-x86_64 ;;
	*)
		echo "build.sh: unknown option $arg" >&2
		exit 2
		;;
	esac
done

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
	-target "$target" \
	-androidapi 24 \
	-javapkg gg.nanolathe.mobile \
	-trimpath \
	-ldflags='-s -w' \
	-o android/app/libs/nanolathe.aar \
	"./$gen/nanolathe"

gradle=gradle
if [ -x android/gradlew ]; then
	gradle=./gradlew
fi
# clean: incremental packaging leaves the old library's bytes as holes in the APK.
(cd android && $gradle --no-daemon -PnanolatheAbis="$abi" clean "assemble$variant")

lower=$(echo "$variant" | tr '[:upper:]' '[:lower:]')
apk=$(ls android/app/build/outputs/apk/"$lower"/*.apk | head -1)
cp "$apk" "build/android/nanolathe-$lower$suffix.apk"
echo "build/android/nanolathe-$lower$suffix.apk"
