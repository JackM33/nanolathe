package io.github.jackm33.nanolathe;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Intent;
import android.database.Cursor;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
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
import android.view.MotionEvent;
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
    private Button installButton;

    // ---- TA: Escalation ----

    /** The catalogue mod the app installs and selects for Escalation players. */
    private static final String ESCALATION_ID = "escalation";
    private static final String ESCALATION_VERSION = "10.2.0";
    private static final int ESCALATION_MB = 345;
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
            info.setText("Game files are copied. Tap = left click (tap to skip the intro). "
                    + "Drag one finger = move the map. "
                    + "Hold until it buzzes, then drag = selection box; hold and let go = right click. "
                    + "Two fingers = zoom. Hold SHIFT (bottom right) while tapping to queue orders. "
                    + "The keyboard opens by itself in text boxes. Back = Escape.");
        } else {
            info.setText("Copy your Total Annihilation folder onto this phone (for example with a USB cable), "
                    + "then choose it here. Pick the folder that contains totala1.hpi. "
                    + "Its files are copied into the app, so you can delete your copy afterwards.");
        }
        column.addView(info);

        if (gameReady()) {
            TextView mod = new TextView(this);
            mod.setTextSize(TypedValue.COMPLEX_UNIT_SP, 15);
            mod.setTypeface(Typeface.DEFAULT_BOLD);
            String saved = Nanolathe.savedMod(getFilesDir().getAbsolutePath());
            mod.setText(saved.isEmpty() ? "Game: plain Total Annihilation" : "Game: " + modName(saved));
            column.addView(mod);
        }

        LinearLayout buttons = new LinearLayout(this);
        buttons.setOrientation(LinearLayout.HORIZONTAL);
        startButton = new Button(this);
        startButton.setText("Start game");
        startButton.setEnabled(gameReady() && !Nanolathe.modBusy());
        startButton.setOnClickListener(v -> startGame());
        buttons.addView(startButton);
        chooseButton = new Button(this);
        chooseButton.setText(gameReady() ? "Choose a different folder" : "Choose game folder");
        chooseButton.setOnClickListener(v -> chooseFolder());
        buttons.addView(chooseButton);
        if (gameReady() && !Nanolathe.savedMod(getFilesDir().getAbsolutePath()).startsWith(ESCALATION_ID + "@")) {
            installButton = new Button(this);
            installButton.setText("Install TA: Escalation (" + ESCALATION_MB + " MB)");
            installButton.setOnClickListener(v -> installEscalation());
            installButton.setEnabled(!Nanolathe.modBusy());
            buttons.addView(installButton);
        }
        if (logFile().exists()) {
            Button copy = new Button(this);
            copy.setText("Copy log");
            copy.setOnClickListener(v -> copyLog());
            buttons.addView(copy);
            Button share = new Button(this);
            share.setText("Share log");
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
                final List<Entry> files = new ArrayList<>();
                list(tree, DocumentsContract.getTreeDocumentId(tree), "", files);
                boolean archives = false;
                int escalation = 0;
                for (Entry e : files) {
                    String lower = e.path.toLowerCase(Locale.ROOT);
                    archives |= lower.endsWith(".hpi") || lower.endsWith(".ufo") || lower.endsWith(".ccx") || lower.endsWith(".gp3");
                    if (isEscalation(e.path)) {
                        escalation++;
                    }
                }
                if (!archives) {
                    throw new IOException("That folder has no .hpi, .ufo, .ccx or .gp3 files. "
                            + "Choose the folder Total Annihilation is installed in, the one with totala1.hpi.");
                }
                if (escalation == 0) {
                    copyFiles(files, false, false);
                    return;
                }
                final int found = escalation;
                main.post(() -> new AlertDialog.Builder(this)
                        .setTitle("TA: Escalation found")
                        .setMessage("This folder has " + found + " TA: Escalation files copied over Total Annihilation. "
                                + "Nanolathe cannot use Escalation installed that way: it mixes the two and leaves "
                                + "build buttons blank.\n\nRecommended: copy plain Total Annihilation, then install "
                                + "Nanolathe's own TA: Escalation package (Gold " + ESCALATION_VERSION + ", a "
                                + ESCALATION_MB + " MB download), which is set up for this engine and becomes the game you play.")
                        .setCancelable(false)
                        .setPositiveButton("Escalation (recommended)", (d, w) -> new Thread(() -> copyFiles(files, true, true)).start())
                        .setNeutralButton("Plain TA only", (d, w) -> new Thread(() -> copyFiles(files, true, false)).start())
                        .setNegativeButton("Copy everything", (d, w) -> new Thread(() -> copyFiles(files, false, false)).start())
                        .show());
            } catch (Exception ex) {
                copyFailed(ex);
            }
        }).start();
    }

    /**
     * TA: Escalation's archives and renamed content folders. Copied over a
     * Total Annihilation install they replace shared art without the renamed
     * folders being read, which leaves build buttons blank
     * (docs/ESCALATION_SUPPORT.md, "Unconfigured upstream installations").
     */
    static boolean isEscalation(String path) {
        String lower = path.toLowerCase(Locale.ROOT);
        int slash = lower.indexOf('/');
        String top = slash >= 0 ? lower.substring(0, slash) : "";
        String name = lower.substring(lower.lastIndexOf('/') + 1);
        if (name.equals("taesc.gp3") || name.matches("t[0-9x]esc[0-9]*\\.ufo")) {
            return true;
        }
        switch (top) {
            case "unitse":
            case "weapone":
            case "gamedate":
            case "unitpice":
            case "guie":
            case "downloadse":
            case "ae":
                return true;
            default:
                return false;
        }
    }

    /** Files the engine never reads: crash logs and Windows programs. */
    static boolean isJunk(String path) {
        String name = path.toLowerCase(Locale.ROOT);
        name = name.substring(name.lastIndexOf('/') + 1);
        return name.startsWith("errorlog") || name.endsWith(".exe") || name.endsWith(".dll");
    }

    /**
     * Copies the chosen files into a staging folder and swaps it in. A file
     * already in the previous copy at the same path and size is moved rather
     * than read again, so choosing the same folder again is quick.
     */
    private void copyFiles(List<Entry> all, boolean skipEscalation, boolean thenEscalation) {
        try {
            List<Entry> files = new ArrayList<>();
            long total = 0;
            int skipped = 0;
            for (Entry e : all) {
                if (isJunk(e.path) || skipEscalation && isEscalation(e.path)) {
                    skipped++;
                    continue;
                }
                files.add(e);
                total += e.size;
            }
            File staging = new File(getFilesDir(), GAME_FOLDER + ".copying");
            deleteTree(staging);
            File previous = gameDir();
            // The previous copy gives up files below, so it is no longer whole.
            //noinspection ResultOfMethodCallIgnored
            new File(previous, READY_MARKER).delete();
            long reusable = 0;
            for (Entry e : files) {
                File old = new File(previous, e.path);
                if (e.size > 0 && old.isFile() && old.length() == e.size) {
                    reusable += e.size;
                }
            }
            if (getFilesDir().getUsableSpace() < total - reusable + (64L << 20)) {
                throw new IOException(String.format(Locale.ROOT, "Not enough free space: the folder needs %d MB more.", (total - reusable) >> 20));
            }
            long done = 0;
            long shownAt = 0;
            int reused = 0;
            byte[] buffer = new byte[1 << 20];
            for (Entry e : files) {
                File out = new File(staging, e.path);
                File parent = out.getParentFile();
                if (parent != null && !parent.isDirectory() && !parent.mkdirs()) {
                    throw new IOException("Could not create " + parent);
                }
                File old = new File(previous, e.path);
                if (e.size > 0 && old.isFile() && old.length() == e.size && old.renameTo(out)) {
                    reused++;
                    done += e.size;
                    continue;
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
            deleteTree(previous);
            if (!staging.renameTo(previous)) {
                throw new IOException("Could not move the copied files into place.");
            }
            final String summary = String.format(Locale.ROOT, "Copied %d files (%d MB), %d reused from the last copy, %d skipped.",
                    files.size(), total >> 20, reused, skipped);
            main.post(() -> {
                copying = false;
                showLauncher(summary + (engineStarted ? " Close and reopen the app to play." : ""));
                if (thenEscalation) {
                    installEscalation();
                }
            });
        } catch (Exception ex) {
            copyFailed(ex);
        }
    }

    private void copyFailed(Exception ex) {
        final String why = ex.getMessage() != null ? ex.getMessage() : ex.toString();
        main.post(() -> {
            copying = false;
            showLauncher("Copy failed: " + why);
        });
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

    /**
     * Installs Nanolathe's TA: Escalation package over the copied game and
     * selects it, as the Mods screen's Get more mods does (docs/ANDROID.md §2).
     */
    private void installEscalation() {
        if (!gameReady() || Nanolathe.modBusy()) {
            return;
        }
        try {
            Nanolathe.installMod(getFilesDir().getAbsolutePath(), gameDir().getAbsolutePath(), ESCALATION_ID);
        } catch (Exception e) {
            if (status != null) {
                status.setText("Could not start the install: " + e.getMessage());
            }
            return;
        }
        if (startButton != null) {
            startButton.setEnabled(false);
        }
        if (installButton != null) {
            installButton.setEnabled(false);
        }
        main.post(watchModInstall);
    }

    private final Runnable watchModInstall = new Runnable() {
        @Override
        public void run() {
            String text = Nanolathe.modStatus();
            if (Nanolathe.modBusy()) {
                if (status != null) {
                    status.setText(text);
                }
                main.postDelayed(this, 500);
                return;
            }
            showLauncher(text + (engineStarted ? " Close and reopen the app to play." : ""));
        }
    };

    private static String modName(String selector) {
        if (selector.startsWith(ESCALATION_ID + "@")) {
            return "TA: Escalation " + selector.substring(ESCALATION_ID.length() + 1);
        }
        return selector;
    }

    // ---- Logs ----

    private void shareLog() {
        Intent send = new Intent(Intent.ACTION_SEND);
        send.setType("text/plain");
        send.putExtra(Intent.EXTRA_SUBJECT, "Nanolathe log");
        send.putExtra(Intent.EXTRA_TEXT, logText());
        startActivity(Intent.createChooser(send, "Share log"));
    }

    private void copyLog() {
        ClipboardManager clipboard = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
        clipboard.setPrimaryClip(ClipData.newPlainText("Nanolathe log", logText()));
        if (status != null) {
            status.setText("Log copied. Paste it into the chat.");
        }
    }

    /**
     * The log's start, which lists the game folder and loaded archives, and its
     * end, where a failure is written.
     */
    private String logText() {
        File log = logFile();
        if (log.length() <= (32 << 10)) {
            return readTail(log, 32 << 10);
        }
        return readHead(log, 12 << 10) + "\n...\n" + readTail(log, 20 << 10);
    }

    private static String readHead(File file, int keep) {
        try (FileInputStream in = new FileInputStream(file)) {
            byte[] data = new byte[keep];
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
        root.addView(shiftButton(), new FrameLayout.LayoutParams(dp(76), dp(48), Gravity.END | Gravity.BOTTOM));
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
        main.postDelayed(watchKeyboard, KEYBOARD_POLL_MS);
    }

    /**
     * Holds Shift while a finger is on it, so another finger's taps queue
     * orders. The layout splits multi-touch, so that finger still reaches the
     * game view (docs/ANDROID.md §3).
     */
    private View shiftButton() {
        final TaButtonDrawable face = new TaButtonDrawable(Math.max(1, dp(1)));
        shiftFace = face;
        final TextView button = new TextView(this);
        button.setText("SHIFT");
        button.setTextSize(TypedValue.COMPLEX_UNIT_SP, 14);
        button.setTypeface(Typeface.create(Typeface.MONOSPACE, Typeface.BOLD));
        button.setLetterSpacing(0.08f);
        button.setGravity(Gravity.CENTER);
        button.setTextColor(0xFFFFFFFF);
        button.setShadowLayer(1, 1, 1, 0xFF000000);
        button.setBackground(face);
        button.setOnTouchListener((v, event) -> {
            switch (event.getActionMasked()) {
                case MotionEvent.ACTION_DOWN:
                    Nanolathe.setShift(true);
                    face.setPressed(true);
                    button.setTextColor(0xFFFFF7A0);
                    return true;
                case MotionEvent.ACTION_UP:
                case MotionEvent.ACTION_CANCEL:
                    Nanolathe.setShift(false);
                    face.setPressed(false);
                    button.setTextColor(0xFFFFFFFF);
                    return true;
                default:
                    return true;
            }
        });
        return button;
    }

    // ---- The soft keyboard ----

    private static final long KEYBOARD_POLL_MS = 300;
    private TaButtonDrawable shiftFace;
    private long shiftFaceStamp;
    private long editorServices = -1;
    private int quietPolls;
    private boolean keyboardWanted;

    /**
     * Opens the keyboard when a text box starts capturing input and closes it
     * when none is, by watching the engine's count of widget passes with a
     * captured editor (docs/ANDROID.md §3). Closing it by hand is respected
     * until the next text box.
     */
    private final Runnable watchKeyboard = new Runnable() {
        @Override
        public void run() {
            if (gameView == null) {
                return;
            }
            refreshShiftFace();
            long n = Nanolathe.editorServices();
            boolean active = editorServices >= 0 && n != editorServices;
            editorServices = n;
            quietPolls = active ? 0 : quietPolls + 1;
            if (active && !keyboardWanted) {
                keyboardWanted = true;
                showKeyboard();
            } else if (keyboardWanted && quietPolls >= 2) {
                keyboardWanted = false;
                hideKeyboard();
            }
            main.postDelayed(this, KEYBOARD_POLL_MS);
        }
    };

    /**
     * Gives the SHIFT button the battle strip's metal once the engine has
     * shared it, and again whenever a battle on another side replaces it.
     */
    private void refreshShiftFace() {
        File art = new File(getFilesDir(), "ui/button-metal.png");
        long stamp = art.lastModified();
        if (shiftFace == null || stamp == 0 || stamp == shiftFaceStamp) {
            return;
        }
        shiftFaceStamp = stamp;
        Bitmap bitmap = BitmapFactory.decodeFile(art.getAbsolutePath());
        if (bitmap != null) {
            shiftFace.setFace(bitmap);
        }
    }

    private void showKeyboard() {
        if (gameView == null) {
            return;
        }
        InputMethodManager imm = (InputMethodManager) getSystemService(INPUT_METHOD_SERVICE);
        gameView.setKeyboard(true);
        gameView.requestFocus();
        imm.restartInput(gameView);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R && getWindow().getInsetsController() != null) {
            getWindow().getInsetsController().show(WindowInsets.Type.ime());
        } else {
            imm.showSoftInput(gameView, InputMethodManager.SHOW_IMPLICIT);
        }
    }

    private void hideKeyboard() {
        if (gameView == null || !gameView.keyboard()) {
            return;
        }
        InputMethodManager imm = (InputMethodManager) getSystemService(INPUT_METHOD_SERVICE);
        gameView.setKeyboard(false);
        imm.hideSoftInputFromWindow(gameView.getWindowToken(), 0);
        imm.restartInput(gameView);
        hideSystemBars();
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
            Nanolathe.setShift(false);
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
