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

// SetShift holds Shift while the on-screen Shift button is down.
func SetShift(held bool) {
	app.AndroidSetShift(held)
}

// EditorServices rises while a text box is capturing input. The app polls it
// to open and close the soft keyboard.
func EditorServices() int64 {
	return app.AndroidEditorServices()
}

// InstallMod starts installing catalogue mod id (such as "escalation") over
// the copied game folder and saves it as the mod to mount. It returns at
// once; ModStatus and ModBusy report progress.
func InstallMod(filesDir, gameDir, id string) error {
	return app.AndroidInstallMod(filesDir, gameDir, id)
}

// ModStatus is the running or last mod install's progress, or "".
func ModStatus() string {
	return app.AndroidModStatus()
}

// ModBusy reports a mod install in progress.
func ModBusy() bool {
	return app.AndroidModBusy()
}

// SavedMod is the mod the game will mount, as id@version, or "".
func SavedMod(filesDir string) string {
	return app.AndroidSavedMod(filesDir)
}

// Failure is the log tail of a start that stopped before or outside its
// window, or "" while the game is running.
func Failure() string {
	return app.AndroidFailure()
}
