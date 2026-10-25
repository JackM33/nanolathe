// Package nanolathe is the Go API the Android app binds with ebitenmobile
// (docs/ANDROID.md §2). tools/android/build.sh copies it beside a generated,
// importable copy of cmd/nanolathe and binds it from there; the leading
// underscore keeps this directory out of ./... builds, which have no such
// copy.
package nanolathe

import app "github.com/nanolathe-gg/nanolathe/build/android/gen/nanolatheapp"

// Start launches the game shell on the copied game folder. MainActivity calls
// it once per process, after showing its EbitenView.
func Start(filesDir, gameDir string) error {
	return app.StartAndroid(filesDir, gameDir)
}

// Failure is the log tail of a start that stopped before or outside its
// window, or "" while the game is running.
func Failure() string {
	return app.AndroidFailure()
}
