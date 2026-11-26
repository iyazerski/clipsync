package dev.iyazerski.clipsync

import android.content.Context
import android.os.Build
import android.provider.Settings as SystemSettings
import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject

/** The name the user gave this phone, as Bluetooth shows it. */
fun phoneName(context: Context): String =
    SystemSettings.Global.getString(context.contentResolver, SystemSettings.Global.DEVICE_NAME)?.takeIf { it.isNotBlank() } ?: Build.MODEL

/** Another device of the pairing, as this phone last heard of it. [seen] is in Unix ms. */
data class KnownDevice(val id: String, val name: String, val kind: String, val seen: Long, val online: Boolean = false)

/** What the relay knows of the other devices: who's listed, and who unpaired. */
class DeviceNews(val devices: List<KnownDevice>, val left: List<String>)

object Devices {
    const val PATH = "/device"
    const val PROFILE_HEADER = "x-clipsync-profile"
    /** The event stream's event name for [DeviceNews]. */
    const val EVENT = "devices"
    /** How long the list keeps a device that hasn't connected. */
    private const val FORGET_AFTER_MS = 30L * 24 * 60 * 60 * 1000

    /** Read the relay's news, skipping [self] and profiles that don't open. */
    fun parse(seal: Seal, self: String, data: String, now: Long): DeviceNews {
        val json = JSONObject(data)
        val list = json.optJSONArray("devices") ?: JSONArray()
        val devices = (0 until list.length()).mapNotNull { i ->
            val d = list.getJSONObject(i)
            val id = d.getString("id")
            val profile = seal.openProfile(id, d.getString("profile"))
            if (id == self || profile == null) return@mapNotNull null
            val online = d.optBoolean("online")
            KnownDevice(id, profile.name, profile.kind, if (online) now else d.optLong("seen"), online)
        }
        val left = json.optJSONArray("left") ?: JSONArray()
        return DeviceNews(devices, (0 until left.length()).map(left::getString))
    }

    /** [known] updated with [news]: the online devices first, then the most recently seen. */
    fun merge(known: List<KnownDevice>, news: DeviceNews, now: Long): List<KnownDevice> {
        val byId = known.associateBy { it.id }.mapValues { (_, d) -> d.copy(online = false) } + news.devices.associateBy { it.id }
        return byId.values
            .filter { it.id !in news.left && now - it.seen <= FORGET_AFTER_MS }
            .sortedWith(compareByDescending<KnownDevice> { it.online }.thenByDescending { it.seen }.thenBy { it.name })
    }

    /** [known] once this phone can no longer tell who's online: they were, until [now]. */
    fun lostTouch(known: List<KnownDevice>, now: Long): List<KnownDevice> =
        known.map { if (it.online) it.copy(online = false, seen = now) else it }

    fun encode(devices: List<KnownDevice>): String = JSONArray(
        devices.map { JSONObject().put("id", it.id).put("name", it.name).put("kind", it.kind).put("seen", it.seen).put("online", it.online) },
    ).toString()

    fun decode(text: String?): List<KnownDevice> = try {
        val list = JSONArray(text ?: return emptyList())
        (0 until list.length()).map { i ->
            val d = list.getJSONObject(i)
            KnownDevice(d.getString("id"), d.getString("name"), d.getString("kind"), d.getLong("seen"), d.optBoolean("online"))
        }
    } catch (e: JSONException) {
        emptyList()
    }
}
