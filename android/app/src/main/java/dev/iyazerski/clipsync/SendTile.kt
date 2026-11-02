package dev.iyazerski.clipsync

import android.service.quicksettings.Tile
import android.service.quicksettings.TileService

/**
 * A Quick Settings tile that sends the clipboard to the Macs in one tap. It also tells the app
 * whether it's in Quick Settings: Android has no call to ask that.
 */
class SendTile : TileService() {
    override fun onTileAdded() {
        super.onTileAdded()
        Settings(this).tileAdded = true
    }

    override fun onTileRemoved() {
        Settings(this).tileAdded = false
        super.onTileRemoved()
    }

    override fun onStartListening() {
        super.onStartListening()
        // Only a tile that's in Quick Settings listens; this also covers one added before the app tracked it.
        Settings(this).tileAdded = true
        qsTile?.apply {
            state = Tile.STATE_INACTIVE
            updateTile()
        }
    }

    override fun onClick() {
        super.onClick()
        startActivityAndCollapse(SendActivity.clipboardIntent(this))
    }
}
