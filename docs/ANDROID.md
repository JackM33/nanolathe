# Android host

Nanolathe runs on Android phones as a sideloaded app. The engine, rules and
renderers are the desktop ones; this document owns only what the Android host
adds: how the app starts the engine, how touch becomes mouse input, and how the
app is built. Like the desktop build, the app ships no game content: the player
copies their own Total Annihilation folder onto the phone.

This is a personal-fork port in its first stage. Nothing here changes what a
tick computes.

## 1. Boundary

| Piece | Owner |
| --- | --- |
| Game loop hand-off | `internal/platform/ebitenapp/run_game_android.go` |
| Touch translation (every platform) | `internal/platform/ebitenapp/touch.go` |
| Engine start-up on Android | `cmd/nanolathe/android.go` |
| Bound Go API | `android/_gobind/nanolathe.go` |
| Launcher, folder copy, view lifecycle | `android/app/src/main/java/.../MainActivity.java` |
| Build | `tools/android/build.sh` |

## 2. Start-up

Ebitengine on Android has no `RunGame`. The Java `EbitenView` drives `Update`
and `Draw` from its render thread once the game is handed over with
`mobile.SetGame`. `ebitenapp.Run` reaches the hand-off through `runGame`, which
on Android sets the game and then blocks until the game reports an error, so
everything `Run` set up stays alive while the view runs. A Termination ends the
process from the view's side.

`MainActivity` is the first screen on every launch. It:

1. copies a folder the player picks with the system folder picker into
   `files/TotalAnnihilation`, because the engine reads content by path and
   Android lends shared folders only through content URIs. The copy is staged
   and renamed into place, then marked complete;
2. on **Start game**, shows a `GameView` (an `EbitenView` that also accepts
   the soft keyboard) and calls `Nanolathe.start` with the display's size in
   physical pixels. `StartAndroid` points `HOME` and the `XDG_*` directories
   at the app's private storage (Go reports `/sdcard` as an Android home),
   opens `files/nanolathe.log` for start-up output and crash traces, fits the
   battle size to the screen once (below), and runs the ordinary command path
   with `--root` set to the copied folder on its own goroutine;
3. polls `Nanolathe.failure` and returns to the launcher with the log tail if
   the start fails, and offers **Share last log** for reporting.

The engine starts once per process. A second start asks the player to reopen
the app.

**Battle size.** Once per install, start-up saves the battle size as the
screen's shape at the minimum battle height of 480, for example 1067x480 on a
20:9 phone, so a battle fills the screen with no letterbox bars and the
sidebar and bars are as large as the authored chrome can be drawn (the Modern
UI scale magnifies only from twice that height). A marker file records the
fit, so a size later chosen in Graphics is kept. Menus keep their authored
640x480 canvas and stay letterboxed.

Back is delivered to the game as Escape. The activity keeps the screen on,
hides the system bars and suspends and resumes the view with its own
lifecycle.

## 3. Touch

Touch screens have no hover and no second button. `touchAdapter` turns finger
gestures into the mouse the rest of the host already reads; it is Nanolathe
host policy, not retail behaviour.

| Gesture | Mouse |
| --- | --- |
| Tap | Left click where the finger first touched |
| Hold still for 450 ms | Right click |
| Move one finger past 8 logical pixels | Left drag from where it first touched |
| Two fingers | Direct pan and pinch, the fields a trackpad fills (DESIGN_GPU_RENDERER §16.6) |
| A finger in the letterbox or within 4 pixels of the canvas edge | Pointer only, so the camera edge-scrolls |

A synthesized press lasts 70 ms so that it spans two 30 Hz host services, and a
drag's first service sees its press at the starting point. A lifted pointer
parks at least 4 pixels inside the canvas so it does not keep edge-scrolling.
Pinch magnification is half the log of the finger distance ratio, because the
battle's smooth pinch doubles a trackpad's magnification; the map then stays
under the fingers. Pan and pinch reach the camera only in the Enhanced
renderer, as trackpad gestures do.

Samples pass through unchanged until a finger touches and again once a real
mouse moves, so desktop and browser input is unaffected. Android starts owned
by touch, because its idle cursor reads (0, 0) and would edge-scroll.

**Soft keyboard.** An **abc** button at the right edge toggles the soft
keyboard. `GameView` then reports itself as a text editor whose connection
keeps no text: committed and composed text goes to the engine through
`Nanolathe.typeText`, and Backspace, Delete, Enter, Escape and Tab through
`Nanolathe.pressKey`. `InjectText` and `InjectKey` in `ebitenapp` add the
characters to the next poll and hold each key for 70 ms with a 40 ms gap
before the next, because a soft keyboard taps its keys inside one frame,
which polling can miss. The field uses the visible-password input type so
the keyboard neither composes words nor corrects them. On Android 11 and
later the game view shrinks above the keyboard so the field stays visible.

Not yet covered: on-screen Shift and Ctrl for queueing and groups, a chrome
larger than the authored one, and hover-only help.

## 4. Building

`tools/android/build.sh` needs `ANDROID_HOME` with an SDK platform (35), build
tools and an NDK, plus a JDK 17 or newer. It:

1. copies `cmd/nanolathe` without tests into `build/android/gen/nanolatheapp`
   and renames its package, because ebitenmobile binds an importable package
   and a command is not one. The copy exists only during the build;
2. installs the `ebitenmobile` matching `go.mod`'s Ebitengine outside the
   module, so its tool dependencies never reach `go.mod`;
3. binds `android/_gobind` for `android/arm64` into
   `android/app/libs/nanolathe.aar`;
4. assembles the app with Gradle and copies the APK to
   `build/android/nanolathe-debug.apk` (`--release` gives an unminified release
   APK signed with the debug key).

`--emulator` binds `android/amd64` instead and writes
`build/android/nanolathe-debug-x86_64.apk`, which runs natively on an x86_64
Android emulator image.

Install the APK by opening it on the phone and allowing installs from that
source. An update installs over the app only when it is signed with the same
key, which is the building machine's debug key; a build from another machine
must be installed after uninstalling, which also removes the copied game
folder.

The desktop builds come from the same branch with the ordinary
`go build ./cmd/nanolathe` (and `GOOS=windows` for Windows); the touch and
keyboard adapters pass desktop input through unchanged.
