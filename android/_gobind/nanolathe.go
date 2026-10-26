// Package nanolathe is the Go API the Android app binds with ebitenmobile
// (docs/ANDROID.md §2). tools/android/build.sh copies it beside a generated,
// importable copy of cmd/nanolathe and binds it from there; the leading
// underscore keeps this directory out of ./... builds, which have no such
// copy.
package nanolathe

import app "github.com/nanolathe-gg/nanolathe/build/android/gen/nanolatheapp"

// Start launches the game shell on the copied game folder. MainActivity calls
// it once per process, after showing its EbitenView, with the display's size
// in physical pixels.
func Start(filesDir, gameDir string, screenWidth, screenHeight int) error {
	return app.StartAndroid(filesDir, gameDir, screenWidth, screenHeight)
}

// TypeText delivers text the soft keyboard committed.
func TypeText(text string) {
	app.AndroidTypeText(text)
}

// PressKey presses one editing key by name: backspace, delete, enter,
// escape or tab. It reports whether the name is known.
func PressKey(name string) bool {
	return app.AndroidPressKey(name)
}

// Failure is the log tail of a start that stopped before or outside its
// window, or "" while the game is running.
func Failure() string {
	return app.AndroidFailure()
}
