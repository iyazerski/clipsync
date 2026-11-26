package dev.iyazerski.clipsync

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdaterTest {
    @Test
    fun versionsCompareByNumber() {
        assertTrue(isNewer("0.2.0", "0.1.20"))
        assertTrue(isNewer("0.2.10", "0.2.9"))
        assertFalse(isNewer("0.2.0", "0.2.0"))
        assertFalse(isNewer("0.1.21", "0.2.0"))
        assertTrue(isNewer("0.2.0", "0.0.0"))
        assertFalse(isNewer("v0.2.x", "0.1.0"))
    }
}
