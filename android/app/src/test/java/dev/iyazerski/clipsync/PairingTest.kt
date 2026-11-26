package dev.iyazerski.clipsync

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Test
import java.security.GeneralSecurityException

class PairingTest {
    private fun hex(s: String) = ByteArray(s.length / 2) { s.substring(it * 2, it * 2 + 2).toInt(16).toByte() }

    /** The test vectors of draft-irtf-cfrg-cpace-21, appendix B.1. */
    @Test
    fun cpaceMatchesTheDraft() {
        val sid = hex("7e4b4791d6a8ef019b936c79fb7f2c57")
        val g = CPace.generator("Password".toByteArray(), hex("0b415f696e69746961746f720b425f726573706f6e646572"), sid)
        assertArrayEquals(hex("d04bf6d41f6a289632a2e929fa29bebd51092512a7829fdde7d314b62f05a73f"), g)
        val a = CPace.Share(g, hex("21b4f4bd9e64ed355c3eb676a28ebedaf6d8f17bdc365995b319097153044080"))
        val b = CPace.Share(g, hex("848b0779ff415f0af4ea14df9dd1d3c29ac41d836c7808896c4eba19c51ac40a"))
        assertArrayEquals(hex("1d13c89278cdadd826f6d8d7f887701430f8380ddc17611cdd6dc989ce0c9f32"), a.public)
        assertArrayEquals(hex("248cccf6d5cdc3646f0ad593f9e6cef4e69d4945f8372e623512ecea32185623"), b.public)
        val isk = hex(
            "6e19b875f7a561d6b3ca3dbb9ef42ac55de3e717881018204b8922b4d5e53bb2" +
                "aa82c300bea7b65d2b671da71922ddf6472301b79bc270adfa8bf413285f2263",
        )
        val ada = "ADa".toByteArray()
        val adb = "ADb".toByteArray()
        assertArrayEquals(isk, a.isk(sid, b.public, a.public, ada, b.public, adb))
        assertArrayEquals(isk, b.isk(sid, a.public, a.public, ada, b.public, adb))
        assertThrows(GeneralSecurityException::class.java) { a.isk(sid, ByteArray(32), a.public, ada, b.public, adb) }
    }

    @Test
    fun codesAndLinksParseAsTyped() {
        assertEquals("42183059", CodePairing.parse(" 42-183 059 "))
        assertEquals("42183059", CodePairing.parse("٤٢١٨٣٠٥٩"))
        assertNull(CodePairing.parse("4218305"))
        assertNull(CodePairing.parse("42 183 05x"))
        val secret = "A".repeat(43)
        val link = CodePairing.parseLink("https://clip.example.com/sub/pair#$secret")
        assertEquals("https://clip.example.com/sub", link?.relay)
        assertEquals(secret, link?.secret)
        assertNull(CodePairing.parseLink("42 183 059"))
        assertNull(CodePairing.parseLink("https://clip.example.com/#$secret"))
    }
}
