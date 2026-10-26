package io.github.jackm33.nanolathe;

import android.app.Activity;
import android.content.Intent;
import android.database.Cursor;
import android.graphics.Typeface;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.os.SystemClock;
import android.provider.DocumentsContract;
import android.util.DisplayMetrics;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.InputDevice;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowInsets;
import android.view.WindowManager;
import android.view.inputmethod.InputMethodManager;
import android.widget.Button;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.ScrollView;
import android.widget.TextView;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

import gg.nanolathe.mobile.nanolathe.Nanolathe;

/**
 * The launcher screen and the game view (docs/ANDROID.md §2).
 *
 * The launcher copies a Total Annihilation folder the player picks into private
 * storage, because the engine reads files by path and Android only lends
 * shared folders through content URIs. It stays the first screen on every
 * launch so a failed start can always be read and shared.
 */
public class MainActivity extends Activity {
    private static final int PICK_FOLDER = 1;
    private static final String READY_MARKER = ".nanolathe-ready";
    private static final String GAME_FOLDER = "TotalAnnihilation";

    /** The engine starts once per process; a second start needs a fresh one. */
    private static boolean engineStarted;

    private final Handler main = new Handler(Looper.getMainLooper());
    private GameView gameView;
    private boolean imeShown;
    private TextView status;
    private ProgressBar progress;
    private Button startButton;
    private Button chooseButton;
    private boolean copying;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            getWindow().getAttributes().layoutInDisplayCutoutMode =
                    WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES;
        }
        showLauncher(null);
    }

    private File gameDir() {
        return new File(getFilesDir(), GAME_FOLDER);
    }

    private File logFile() {
        return new File(getFilesDir(), "nanolathe.log");
    }

    private boolean gameReady() {
        return new File(gameDir(), READY_MARKER).exists();
    }

    // ---- Launcher ----

    private void showLauncher(String message) {
        gameView = null;
        int pad = dp(16);
        LinearLayout column = new LinearLayout(this);
        column.setOrientation(LinearLayout.VERTICAL);
        column.setPadding(pad, pad, pad, pad);

        TextView title = new TextView(this);
        title.setText("Nanolathe for Android (test build)");
        title.setTextSize(TypedValue.COMPLEX_UNIT_SP, 22);
        title.setTypeface(Typeface.DEFAULT_BOLD);
        column.addView(title);

        TextView info = new TextView(this);
        info.setTextSize(TypedValue.COMPLEX_UNIT_SP, 15);
        info.setPadding(0, dp(8), 0, dp(8));
        if (gameReady()) {
            info.setText("Game files are copied. Tap to play: tap = left click, hold = right click, "
                    + "drag = select box, two fingers = pan and zoom, back = Escape. "
                    + "Touch the screen edges to scroll the map. The abc button on the right opens the keyboard.");
        } else {
            info.setText("Copy your Total Annihilation folder onto this phone (for example with a USB cable), "
                    + "then choose it here. Pick the folder that contains totala1.hpi. "
                    + "Its files are copied into the app, so you can delete your copy afterwards.");
        }
        column.addView(info);

        LinearLayout buttons = new LinearLayout(this);
        buttons.setOrientation(LinearLayout.HORIZONTAL);
        startButton = new Button(this);
        startButton.setText("Start game");
        startButton.setEnabled(gameReady());
        startButton.setOnClickListener(v -> startGame());
        buttons.addView(startButton);
        chooseButton = new Button(this);
        chooseButton.setText(gameReady() ? "Choose a different folder" : "Choose game folder");
        chooseButton.setOnClickListener(v -> chooseFolder());
        buttons.addView(chooseButton);
        if (logFile().exists()) {
            Button share = new Button(this);
            share.setText("Share last log");
            share.setOnClickListener(v -> shareLog());
            buttons.addView(share);
        }
        column.addView(buttons);

        progress = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
        progress.setMax(1000);
        progress.setVisibility(View.GONE);
        column.addView(progress);

        status = new TextView(this);
        status.setTextSize(TypedValue.COMPLEX_UNIT_SP, 13);
        status.setTypeface(Typeface.MONOSPACE);
        status.setPadding(0, dp(8), 0, 0);
        status.setTextIsSelectable(true);
        if (message != null) {
            status.setText(message);
        }
        column.addView(status);

        ScrollView scroll = new ScrollView(this);
        scroll.addView(column);
        setContentView(scroll);
    }

    private int dp(int value) {
        return Math.round(TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, value, getResources().getDisplayMetrics()));
    }

    private void chooseFolder() {
        if (copying) {
            return;
        }
        startActivityForResult(new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE), PICK_FOLDER);
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == PICK_FOLDER && resultCode == RESULT_OK && data != null && data.getData() != null) {
            copyGame(data.getData());
        }
    }

    // ---- Copying the game folder ----

    private static final class Entry {
        final Uri uri;
        final String path;
        final long size;

        Entry(Uri uri, String path, long size) {
            this.uri = uri;
            this.path = path;
            this.size = size;
        }
    }

    private void copyGame(final Uri tree) {
        copying = true;
        startButton.setEnabled(false);
        chooseButton.setEnabled(false);
        progress.setVisibility(View.VISIBLE);
        progress.setProgress(0);
        status.setText("Reading the folder...");
        new Thread(() -> {
            try {
                List<Entry> files = new ArrayList<>();
                list(tree, DocumentsContract.getTreeDocumentId(tree), "", files);
                long total = 0;
                boolean archives = false;
                for (Entry e : files) {
                    total += e.size;
                    String lower = e.path.toLowerCase(Locale.ROOT);
                    archives |= lower.endsWith(".hpi") || lower.endsWith(".ufo") || lower.endsWith(".ccx") || lower.endsWith(".gp3");
                }
                if (!archives) {
                    throw new IOException("That folder has no .hpi, .ufo, .ccx or .gp3 files. "
                            + "Choose the folder Total Annihilation is installed in, the one with totala1.hpi.");
                }
                File staging = new File(getFilesDir(), GAME_FOLDER + ".copying");
                deleteTree(staging);
                if (getFilesDir().getUsableSpace() < total + (64L << 20)) {
                    throw new IOException(String.format(Locale.ROOT, "Not enough free space: the folder needs %d MB.", total >> 20));
                }
                long done = 0;
                long shownAt = 0;
                byte[] buffer = new byte[1 << 20];
                for (Entry e : files) {
                    File out = new File(staging, e.path);
                    File parent = out.getParentFile();
                    if (parent != null && !parent.isDirectory() && !parent.mkdirs()) {
                        throw new IOException("Could not create " + parent);
                    }
                    try (InputStream in = getContentResolver().openInputStream(e.uri);
                         OutputStream os = new FileOutputStream(out)) {
                        if (in == null) {
                            throw new IOException("Could not open " + e.path);
                        }
                        int n;
                        while ((n = in.read(buffer)) > 0) {
                            os.write(buffer, 0, n);
                            done += n;
                            long now = SystemClock.uptimeMillis();
                            if (now - shownAt > 250) {
                                shownAt = now;
                                showCopyProgress(e.path, done, total);
                            }
                        }
                    }
                }
                if (!new File(staging, READY_MARKER).createNewFile()) {
                    throw new IOException("Could not mark the copy complete.");
                }
                deleteTree(gameDir());
                if (!staging.renameTo(gameDir())) {
                    throw new IOException("Could not move the copied files into place.");
                }
                final String summary = String.format(Locale.ROOT, "Copied %d files (%d MB).", files.size(), total >> 20);
                main.post(() -> {
                    copying = false;
                    showLauncher(summary + (engineStarted ? " Close and reopen the app to play." : ""));
                });
            } catch (Exception ex) {
                final String why = ex.getMessage() != null ? ex.getMessage() : ex.toString();
                main.post(() -> {
                    copying = false;
                    showLauncher("Copy failed: " + why);
                });
            }
        }).start();
    }

    private void showCopyProgress(final String path, final long done, final long total) {
        main.post(() -> {
            if (progress == null || status == null) {
                return;
            }
            progress.setProgress(total > 0 ? (int) (done * 1000 / total) : 0);
            status.setText(String.format(Locale.ROOT, "Copying %d / %d MB\n%s", done >> 20, total >> 20, path));
        });
    }

    private void list(Uri tree, String documentId, String prefix, List<Entry> out) throws IOException {
        Uri children = DocumentsContract.buildChildDocumentsUriUsingTree(tree, documentId);
        String[] columns = {
                DocumentsContract.Document.COLUMN_DOCUMENT_ID,
                DocumentsContract.Document.COLUMN_DISPLAY_NAME,
                DocumentsContract.Document.COLUMN_MIME_TYPE,
                DocumentsContract.Document.COLUMN_SIZE,
        };
        try (Cursor c = getContentResolver().query(children, columns, null, null, null)) {
            if (c == null) {
                throw new IOException("Could not list " + (prefix.isEmpty() ? "the folder" : prefix));
            }
            while (c.moveToNext()) {
                String id = c.getString(0);
                String name = c.getString(1);
                String mime = c.getString(2);
                long size = c.isNull(3) ? 0 : c.getLong(3);
                // Never let a provider's name step outside the copy.
                if (name == null || name.isEmpty() || name.equals(".") || name.equals("..") || name.contains("/")) {
                    continue;
                }
                if (DocumentsContract.Document.MIME_TYPE_DIR.equals(mime)) {
                    list(tree, id, prefix + name + "/", out);
                } else {
                    out.add(new Entry(DocumentsContract.buildDocumentUriUsingTree(tree, id), prefix + name, size));
                }
            }
        }
    }

    private static void deleteTree(File file) {
        File[] children = file.listFiles();
        if (children != null) {
            for (File child : children) {
                deleteTree(child);
            }
        }
        //noinspection ResultOfMethodCallIgnored
        file.delete();
    }

    // ---- Logs ----

    private void shareLog() {
        Intent send = new Intent(Intent.ACTION_SEND);
        send.setType("text/plain");
        send.putExtra(Intent.EXTRA_SUBJECT, "Nanolathe log");
        send.putExtra(Intent.EXTRA_TEXT, readTail(logFile(), 24 << 10));
        startActivity(Intent.createChooser(send, "Share log"));
    }

    private static String readTail(File file, int keep) {
        try (FileInputStream in = new FileInputStream(file)) {
            long length = file.length();
            if (length > keep) {
                long skipped = in.skip(length - keep);
                if (skipped < 0) {
                    skipped = 0;
                }
            }
            byte[] data = new byte[(int) Math.min(length, keep)];
            int read = 0;
            while (read < data.length) {
                int n = in.read(data, read, data.length - read);
                if (n < 0) {
                    break;
                }
                read += n;
            }
            return new String(data, 0, read, StandardCharsets.UTF_8);
        } catch (IOException e) {
            return "Could not read the log: " + e.getMessage();
        }
    }

    // ---- The game ----

    private void startGame() {
        if (engineStarted) {
            showLauncher("The game already ran in this session. Close and reopen the app to start it again.");
            return;
        }
        if (!gameReady()) {
            return;
        }
        engineStarted = true;
        gameView = new GameView(this);
        FrameLayout root = new FrameLayout(this);
        root.addView(gameView, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        root.addView(keyboardButton(), new FrameLayout.LayoutParams(dp(44), dp(44), Gravity.END | Gravity.CENTER_VERTICAL));
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            // The app places the keyboard itself: the game shrinks above it, so
            // the field being typed into stays visible.
            getWindow().setDecorFitsSystemWindows(false);
            root.setOnApplyWindowInsetsListener((v, insets) -> {
                boolean shown = insets.isVisible(WindowInsets.Type.ime());
                v.setPadding(0, 0, 0, shown ? insets.getInsets(WindowInsets.Type.ime()).bottom : 0);
                if (imeShown && !shown && gameView != null) {
                    gameView.setKeyboard(false);
                    hideSystemBars();
                }
                imeShown = shown;
                return insets;
            });
        }
        setContentView(root);
        hideSystemBars();
        DisplayMetrics metrics = new DisplayMetrics();
        getWindowManager().getDefaultDisplay().getRealMetrics(metrics);
        int longSide = Math.max(metrics.widthPixels, metrics.heightPixels);
        int shortSide = Math.min(metrics.widthPixels, metrics.heightPixels);
        try {
            Nanolathe.start(getFilesDir().getAbsolutePath(), gameDir().getAbsolutePath(), longSide, shortSide);
        } catch (Exception e) {
            showLauncher("The game did not start: " + e.getMessage());
            return;
        }
        main.postDelayed(watchFailure, 1000);
    }

    private View keyboardButton() {
        TextView button = new TextView(this);
        button.setText("abc");
        button.setTextSize(TypedValue.COMPLEX_UNIT_SP, 13);
        button.setGravity(Gravity.CENTER);
        button.setTextColor(0xFFFFFFFF);
        button.setBackgroundColor(0x66000000);
        button.setOnClickListener(v -> toggleKeyboard());
        return button;
    }

    @SuppressWarnings("deprecation")
    private void toggleKeyboard() {
        if (gameView == null) {
            return;
        }
        InputMethodManager imm = (InputMethodManager) getSystemService(INPUT_METHOD_SERVICE);
        if (gameView.keyboard()) {
            gameView.setKeyboard(false);
            imm.hideSoftInputFromWindow(gameView.getWindowToken(), 0);
            imm.restartInput(gameView);
            hideSystemBars();
            return;
        }
        gameView.setKeyboard(true);
        gameView.requestFocus();
        imm.restartInput(gameView);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R && getWindow().getInsetsController() != null) {
            getWindow().getInsetsController().show(WindowInsets.Type.ime());
        } else {
            imm.showSoftInput(gameView, InputMethodManager.SHOW_IMPLICIT);
        }
    }

    /** A start that fails before or outside the window would otherwise be a black screen. */
    private final Runnable watchFailure = new Runnable() {
        @Override
        public void run() {
            if (gameView == null) {
                return;
            }
            String failure = Nanolathe.failure();
            if (failure != null && !failure.isEmpty()) {
                showLauncher("The game stopped:\n\n" + failure);
                return;
            }
            main.postDelayed(this, 1000);
        }
    };

    @SuppressWarnings("deprecation")
    private void hideSystemBars() {
        getWindow().getDecorView().setSystemUiVisibility(
                View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                        | View.SYSTEM_UI_FLAG_FULLSCREEN
                        | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                        | View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                        | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION
                        | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN);
    }

    @Override
    public void onWindowFocusChanged(boolean hasFocus) {
        super.onWindowFocusChanged(hasFocus);
        if (hasFocus && gameView != null) {
            hideSystemBars();
        }
    }

    @Override
    protected void onPause() {
        super.onPause();
        if (gameView != null) {
            gameView.suspendGame();
        }
    }

    @Override
    protected void onResume() {
        super.onResume();
        if (gameView != null) {
            gameView.resumeGame();
            hideSystemBars();
        }
    }

    /** Total Annihilation has no back button; Escape closes menus and cancels. */
    @Override
    public boolean dispatchKeyEvent(KeyEvent event) {
        if (gameView != null && event.getKeyCode() == KeyEvent.KEYCODE_BACK) {
            return super.dispatchKeyEvent(new KeyEvent(event.getDownTime(), event.getEventTime(), event.getAction(),
                    KeyEvent.KEYCODE_ESCAPE, event.getRepeatCount(), event.getMetaState(), event.getDeviceId(),
                    event.getScanCode(), event.getFlags(), InputDevice.SOURCE_KEYBOARD));
        }
        return super.dispatchKeyEvent(event);
    }
}
