package dev.iyazerski.clipsync

import android.service.quicksettings.Tile
import android.service.quicksettings.TileService

/** A Quick Settings tile that sends the clipboard to the Macs in one tap. */
class SendTile : TileService() {
    override fun onStartListening() {
        super.onStartListening()
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
