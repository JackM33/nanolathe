package io.github.jackm33.nanolathe;

import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.ColorFilter;
import android.graphics.Paint;
import android.graphics.PixelFormat;
import android.graphics.Rect;
import android.graphics.RectF;
import android.graphics.drawable.Drawable;

/**
 * A button face matching the game's own quick buttons: the battle strip's
 * dark metal art (the engine shares it as a picture, docs/ANDROID.md §3)
 * inside a black outline with a light top-left and dark bottom-right bevel.
 * Pressed, the bevel inverts, as the game's buttons do. Until the engine has
 * shared its art the face is plain dark steel.
 */
final class TaButtonDrawable extends Drawable {
    private static final int FACE = 0xF0262A2E;
    private static final int LIGHT = 0xFFB0B0B0;
    private static final int DARK = 0xFF545454;
    private static final int OUTLINE = 0xFF000000;

    private final Paint paint = new Paint();
    private final Paint art = new Paint();
    private final float bevel;
    private Bitmap face;
    private boolean pressed;

    TaButtonDrawable(float bevelPixels) {
        bevel = bevelPixels;
        // The art is pixel art: scale it without smoothing.
        art.setFilterBitmap(false);
    }

    void setPressed(boolean down) {
        if (pressed != down) {
            pressed = down;
            invalidateSelf();
        }
    }

    void setFace(Bitmap bitmap) {
        face = bitmap;
        invalidateSelf();
    }

    @Override
    public void draw(Canvas canvas) {
        Rect b = getBounds();
        float l = b.left, t = b.top, r = b.right, btm = b.bottom;
        paint.setStyle(Paint.Style.FILL);
        paint.setColor(OUTLINE);
        canvas.drawRect(l, t, r, btm, paint);
        l += bevel; t += bevel; r -= bevel; btm -= bevel;
        // Top and left edges, then bottom and right, each a solid band.
        paint.setColor(pressed ? DARK : LIGHT);
        canvas.drawRect(l, t, r, t + bevel, paint);
        canvas.drawRect(l, t, l + bevel, btm, paint);
        paint.setColor(pressed ? LIGHT : DARK);
        canvas.drawRect(l, btm - bevel, r, btm, paint);
        canvas.drawRect(r - bevel, t, r, btm, paint);
        RectF inner = new RectF(l + bevel, t + bevel, r - bevel, btm - bevel);
        if (face != null) {
            canvas.drawBitmap(face, null, inner, art);
        } else {
            paint.setColor(FACE);
            canvas.drawRect(inner, paint);
        }
    }

    @Override
    public void setAlpha(int alpha) {
        paint.setAlpha(alpha);
        art.setAlpha(alpha);
    }

    @Override
    public void setColorFilter(ColorFilter filter) {
        paint.setColorFilter(filter);
        art.setColorFilter(filter);
    }

    @Override
    public int getOpacity() {
        return PixelFormat.TRANSLUCENT;
    }
}
