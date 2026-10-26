package io.github.jackm33.nanolathe;

import android.content.Context;
import android.text.InputType;
import android.view.KeyEvent;
import android.view.inputmethod.BaseInputConnection;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputConnection;

import gg.nanolathe.mobile.nanolathe.EbitenView;
import gg.nanolathe.mobile.nanolathe.Nanolathe;

/**
 * The game view, plus a soft-keyboard connection (docs/ANDROID.md §3).
 *
 * Text and editing keys go to the engine directly instead of as key events:
 * a soft keyboard commits whole strings and taps its keys inside one frame,
 * which a polled keyboard can miss.
 */
public class GameView extends EbitenView {
    private boolean keyboard;

    public GameView(Context context) {
        super(context);
    }

    /** Whether the soft keyboard should attach to this view. */
    void setKeyboard(boolean on) {
        keyboard = on;
    }

    boolean keyboard() {
        return keyboard;
    }

    @Override
    public boolean onCheckIsTextEditor() {
        return keyboard;
    }

    @Override
    public InputConnection onCreateInputConnection(EditorInfo outAttrs) {
        if (!keyboard) {
            return null;
        }
        // A visible-password field keeps the keyboard from composing words or
        // offering corrections, so each key reaches the game as it is typed.
        outAttrs.inputType = InputType.TYPE_CLASS_TEXT
                | InputType.TYPE_TEXT_VARIATION_VISIBLE_PASSWORD
                | InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS;
        outAttrs.imeOptions = EditorInfo.IME_ACTION_DONE
                | EditorInfo.IME_FLAG_NO_EXTRACT_UI
                | EditorInfo.IME_FLAG_NO_FULLSCREEN;
        return new Connection(this);
    }

    /** Forwards the keyboard's edits; it keeps no text of its own. */
    private static final class Connection extends BaseInputConnection {
        /** Text the keyboard is still composing, already typed into the game. */
        private String composing = "";

        Connection(GameView view) {
            super(view, false);
        }

        /** Brings the game's text from the composing string to {@code text}. */
        private void replaceComposing(String text) {
            int common = 0;
            int limit = Math.min(composing.length(), text.length());
            while (common < limit && composing.charAt(common) == text.charAt(common)) {
                common++;
            }
            for (int i = composing.length(); i > common; i--) {
                Nanolathe.pressKey("backspace");
            }
            if (common < text.length()) {
                Nanolathe.typeText(text.substring(common));
            }
            composing = text;
        }

        @Override
        public boolean commitText(CharSequence text, int newCursorPosition) {
            replaceComposing(text.toString());
            composing = "";
            return true;
        }

        @Override
        public boolean setComposingText(CharSequence text, int newCursorPosition) {
            replaceComposing(text.toString());
            return true;
        }

        @Override
        public boolean finishComposingText() {
            composing = "";
            return true;
        }

        @Override
        public boolean deleteSurroundingText(int beforeLength, int afterLength) {
            for (int i = 0; i < beforeLength; i++) {
                Nanolathe.pressKey("backspace");
            }
            for (int i = 0; i < afterLength; i++) {
                Nanolathe.pressKey("delete");
            }
            return true;
        }

        @Override
        public boolean sendKeyEvent(KeyEvent event) {
            if (event.getAction() != KeyEvent.ACTION_DOWN) {
                return true;
            }
            switch (event.getKeyCode()) {
                case KeyEvent.KEYCODE_DEL:
                    Nanolathe.pressKey("backspace");
                    break;
                case KeyEvent.KEYCODE_FORWARD_DEL:
                    Nanolathe.pressKey("delete");
                    break;
                case KeyEvent.KEYCODE_ENTER:
                case KeyEvent.KEYCODE_NUMPAD_ENTER:
                    Nanolathe.pressKey("enter");
                    break;
                case KeyEvent.KEYCODE_ESCAPE:
                    Nanolathe.pressKey("escape");
                    break;
                case KeyEvent.KEYCODE_TAB:
                    Nanolathe.pressKey("tab");
                    break;
                default:
                    int c = event.getUnicodeChar();
                    if (c != 0) {
                        Nanolathe.typeText(new String(Character.toChars(c)));
                    }
            }
            return true;
        }

        @Override
        public boolean performEditorAction(int editorAction) {
            Nanolathe.pressKey("enter");
            return true;
        }
    }
}
