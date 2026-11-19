package dev.iyazerski.clipsync

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class SealTest {
    private val pairing = checkNotNull(javaClass.getResourceAsStream("/pairing.txt"))
        .bufferedReader().use { it.readLines() }
    private val secret = pairing[0]
    private val token = pairing[1]
    private val sealedAt = 1_700_000_000_000L
    private val sealed = "AgcHBwcHBwcHBwcHB7vfjbeCOIF7IogOfIGZOG33yZnkFUm3yjqv3ibc0DouywRo"
    private val maxAge = 5 * 60 * 1000L

    @Test
    fun theTestVectorsHold() {
        val seal = Seal(secret)
        assertEquals(token, seal.token)
        assertEquals(sealed, seal.sealWith(ByteArray(12) { 7 }, sealedAt, "héllo 👋"))
        assertEquals("héllo 👋", seal.open(sealed, Seal.Opened(), sealedAt))
    }

    @Test
    fun onlyTheSameSecretOpensAClip() {
        val seal = Seal(secret)
        val clip = seal.seal("text")
        assertNotEquals(clip, seal.seal("text"))
        assertEquals("text", seal.open(clip, Seal.Opened()))
        assertThrows(IllegalArgumentException::class.java) { Seal("A".repeat(43)).open(clip, Seal.Opened()) }
        assertThrows(IllegalArgumentException::class.java) { Seal("short") }
    }

    @Test
    fun aClipOpensOnceAndOnlyWhileFresh() {
        val seal = Seal(secret)
        val opened = Seal.Opened()
        assertThrows(IllegalArgumentException::class.java) { seal.open(sealed, opened, sealedAt + maxAge + 1) }
        assertThrows(IllegalArgumentException::class.java) { seal.open(sealed, opened, sealedAt - maxAge - 1) }
        assertEquals("héllo 👋", seal.open(sealed, opened, sealedAt + 1000))
        assertThrows(IllegalArgumentException::class.java) { seal.open(sealed, opened, sealedAt + 2000) }
    }
}
