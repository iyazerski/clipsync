package dev.iyazerski.clipsync

import org.junit.Assert.assertEquals
import org.junit.Test

class DevicesTest {
    private val seal = Seal("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8")
    private val now = 1_700_000_000_000L
    private val day = 24 * 60 * 60 * 1000L

    private fun news(vararg devices: String, left: String = "") =
        """{"devices":[${devices.joinToString(",")}],"left":[$left]}"""

    private fun device(id: String, name: String, kind: String, online: Boolean, seen: Long = 0) =
        """{"id":"$id","profile":"${seal.sealProfile(id, Profile(name, kind))}","online":$online,"seen":$seen}"""

    @Test
    fun theListFollowsTheRelay() {
        val stale = KnownDevice("win-old", "Old PC", "windows", now - 40 * day)
        val first = Devices.parse(
            seal, "phone-1",
            news(
                device("phone-1", "This phone", "android", true),
                device("mac-1", "Mac", "mac", true),
                device("win-1", "PC", "windows", false, now - day),
                // The relay put another device's profile on this one.
                """{"id":"mac-2","profile":"${seal.sealProfile("mac-1", Profile("Fake", "mac"))}","online":true}""",
            ),
            now,
        )
        val known = Devices.merge(listOf(stale), first, now)
        assertEquals(listOf(KnownDevice("mac-1", "Mac", "mac", now, true), KnownDevice("win-1", "PC", "windows", now - day)), known)

        // Offline, the phone remembers when it last saw each device.
        val later = now + 60_000
        val offline = Devices.lostTouch(known, later)
        assertEquals(listOf(later, now - day), offline.map { it.seen })
        assertEquals(listOf(false, false), offline.map { it.online })

        // A device that unpaired goes, even when the relay no longer lists anyone.
        assertEquals(listOf("mac-1"), Devices.merge(offline, Devices.parse(seal, "phone-1", news(left = "\"win-1\""), later), later).map { it.id })
        assertEquals(offline, Devices.decode(Devices.encode(offline)))
    }
}
