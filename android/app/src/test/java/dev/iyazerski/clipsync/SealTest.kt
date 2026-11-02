package dev.iyazerski.clipsync

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class SealTest {
    // Shared with the Mac's src/seal.rs tests: change both together.
    private val secret = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"
    private val token = "eJMfuewcmawfRsgaj7Zutkow2oBVjbx8Thv_-3tJnYE"
    private val sealed = "AQcHBwcHBwcHBwcHB9McJVAhssmL1dosNtILnU4lKTcuyGvlFloSsg"

    @Test
    fun theTestVectorsHold() {
        val seal = Seal(secret)
        assertEquals(token, seal.token)
        assertEquals(sealed, seal.sealWith(ByteArray(12) { 7 }, "héllo 👋"))
        assertEquals("héllo 👋", seal.open(sealed))
    }

    @Test
    fun onlyTheSameSecretOpensAClip() {
        val seal = Seal(secret)
        val clip = seal.seal("text")
        assertNotEquals(clip, seal.seal("text"))
        assertEquals("text", seal.open(clip))
        assertThrows(IllegalArgumentException::class.java) { Seal("A".repeat(43)).open(clip) }
        assertThrows(IllegalArgumentException::class.java) { Seal("short") }
    }
}
