package io.github.jackm33.nanolathe;

import android.graphics.Canvas;
import android.graphics.ColorFilter;
import android.graphics.Paint;
import android.graphics.PixelFormat;
import android.graphics.Rect;
import android.graphics.drawable.Drawable;

/**
 * A bevelled steel button face in the manner of Total Annihilation's side
 * panel buttons, for controls the app draws over the game. Pressed, the bevel
 * inverts and the face lightens, as the game's own buttons do.
 */
final class TaButtonDrawable extends Drawable {
    private static final int FACE = 0xE0444C55;
    private static final int FACE_PRESSED = 0xF0626E7A;
    private static final int LIGHT = 0xFFA7B1BB;
    private static final int DARK = 0xFF15191E;
    private static final int OUTLINE = 0xFF000000;

    private final Paint paint = new Paint();
    private final float bevel;
    private boolean pressed;

    TaButtonDrawable(float bevelPixels) {
        bevel = bevelPixels;
    }

    void setPressed(boolean down) {
        if (pressed != down) {
            pressed = down;
            invalidateSelf();
        }
    }

    @Override
    public void draw(Canvas canvas) {
        Rect b = getBounds();
        float l = b.left, t = b.top, r = b.right, btm = b.bottom;
        paint.setStyle(Paint.Style.FILL);
        paint.setColor(OUTLINE);
        canvas.drawRect(l, t, r, btm, paint);
        l += 1; t += 1; r -= 1; btm -= 1;
        // Top and left edges, then bottom and right, each a solid band.
        paint.setColor(pressed ? DARK : LIGHT);
        canvas.drawRect(l, t, r, t + bevel, paint);
        canvas.drawRect(l, t, l + bevel, btm, paint);
        paint.setColor(pressed ? LIGHT : DARK);
        canvas.drawRect(l, btm - bevel, r, btm, paint);
        canvas.drawRect(r - bevel, t, r, btm, paint);
        paint.setColor(pressed ? FACE_PRESSED : FACE);
        canvas.drawRect(l + bevel, t + bevel, r - bevel, btm - bevel, paint);
    }

    @Override
    public void setAlpha(int alpha) {
        paint.setAlpha(alpha);
    }

    @Override
    public void setColorFilter(ColorFilter filter) {
        paint.setColorFilter(filter);
    }

    @Override
    public int getOpacity() {
        return PixelFormat.TRANSLUCENT;
    }
}
