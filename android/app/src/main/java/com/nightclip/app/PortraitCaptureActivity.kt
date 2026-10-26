package com.nightclip.app

import com.journeyapps.barcodescanner.CaptureActivity

/** The zxing-android-embedded scanner screen; only subclassed so the manifest can let it rotate with the phone. */
class PortraitCaptureActivity : CaptureActivity()
