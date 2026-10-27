package com.nightclip.app.platform

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Matrix
import android.media.ExifInterface
import com.nightclip.app.logic.ImageSampling

/**
 * Decodes a picture attachment for the preview without letting a big photo use the memory of the app: the size is read
 * first (no pixels), then the file is decoded at a reduced size (`inSampleSize`) and turned upright by its EXIF tag.
 * Any failure, an out-of-memory error included, gives null and the preview shows a placeholder.
 */
object AttachmentBitmaps {
    fun decode(path: String, targetWidthPx: Int): Bitmap? {
        return try {
            val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
            BitmapFactory.decodeFile(path, bounds)
            if (bounds.outWidth <= 0 || bounds.outHeight <= 0) return null
            val options = BitmapFactory.Options().apply {
                inSampleSize = ImageSampling.sampleSize(bounds.outWidth, bounds.outHeight, targetWidthPx)
                inPreferredConfig = Bitmap.Config.ARGB_8888
            }
            val bitmap = BitmapFactory.decodeFile(path, options) ?: return null
            upright(bitmap, path)
        } catch (e: Exception) {
            null
        } catch (e: OutOfMemoryError) {
            null
        }
    }

    private fun upright(bitmap: Bitmap, path: String): Bitmap {
        val orientation = try {
            ExifInterface(path).getAttributeInt(ExifInterface.TAG_ORIENTATION, ExifInterface.ORIENTATION_NORMAL)
        } catch (e: Exception) {
            ExifInterface.ORIENTATION_NORMAL
        }
        val t = ImageSampling.exifTransform(orientation)
        if (t.rotation == 0 && !t.flipHorizontal) return bitmap
        val m = Matrix().apply {
            postRotate(t.rotation.toFloat())
            if (t.flipHorizontal) postScale(-1f, 1f)
        }
        return try {
            Bitmap.createBitmap(bitmap, 0, 0, bitmap.width, bitmap.height, m, true)
        } catch (e: OutOfMemoryError) {
            bitmap // upright is nicer than nothing, a sideways picture is better than none
        }
    }
}
