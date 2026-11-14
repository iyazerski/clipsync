package dev.iyazerski.clipsync

import java.nio.ByteBuffer
import java.security.GeneralSecurityException
import java.security.SecureRandom
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.Mac
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec
import kotlin.math.absoluteValue

/**
 * Sealing clips, so the relay passes them on without reading them. The same scheme as the Mac's
 * `src/seal.rs`, which shares its test vectors with SealTest.
 *
 * One secret, shared by your devices, gives the relay token and the sealing key: each is an
 * HMAC-SHA256 of a label, keyed with the secret. A sealed clip is AES-256-GCM,
 * `version || nonce || ciphertext || tag`, in base64url without padding. The sealed text starts with
 * the time it was sealed, so a device refuses clips that are old, or that it opened already: the
 * relay can't replay them.
 */
class Seal(secret: String) {
    /** What the relay checks: it can't work out the key from it. */
    val token: String
    private val key: SecretKeySpec

    init {
        val raw = runCatching { DECODER.decode(secret.trim()) }.getOrNull()
        require(raw != null && raw.size == SECRET_LEN) {
            "The secret should be 43 characters, as `clipsync pair` shows it"
        }
        token = ENCODER.encodeToString(hmac(raw, "clipsync/token"))
        key = SecretKeySpec(hmac(raw, "clipsync/key"), "AES")
    }

    /** Seal [text] with a fresh random nonce and the current time. */
    fun seal(text: String): String =
        sealWith(ByteArray(NONCE_LEN).also(RANDOM::nextBytes), System.currentTimeMillis(), text)

    internal fun sealWith(nonce: ByteArray, sealedAt: Long, text: String): String {
        val cipher = Cipher.getInstance(CIPHER)
        cipher.init(Cipher.ENCRYPT_MODE, key, GCMParameterSpec(TAG_BITS, nonce))
        val bytes = text.toByteArray(Charsets.UTF_8)
        val body = cipher.doFinal(ByteBuffer.allocate(STAMP_LEN + bytes.size).putLong(sealedAt).put(bytes).array())
        return ENCODER.encodeToString(byteArrayOf(VERSION) + nonce + body)
    }

    /**
     * Open a sealed clip at [now] (ms since the Unix epoch) and add it to [opened]. Throws if it was
     * sealed with another secret, changed, sealed too far from [now], or opened already.
     */
    fun open(sealed: String, opened: Opened, now: Long = System.currentTimeMillis()): String {
        val raw = runCatching { DECODER.decode(sealed) }.getOrNull() ?: throw IllegalArgumentException("Not base64url")
        require(raw.size > 1 + NONCE_LEN) { "Clip too short" }
        require(raw[0] == VERSION) { "Clip format ${raw[0]} is unknown: update clipsync" }
        val cipher = Cipher.getInstance(CIPHER)
        cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(TAG_BITS, raw, 1, NONCE_LEN))
        val text = try {
            cipher.doFinal(raw, 1 + NONCE_LEN, raw.size - 1 - NONCE_LEN)
        } catch (e: GeneralSecurityException) {
            throw IllegalArgumentException("Can't open the clip: the devices use different secrets", e)
        }
        require(text.size >= STAMP_LEN) { "Clip too short" }
        val sealedAt = ByteBuffer.wrap(text, 0, STAMP_LEN).long
        val off = (now - sealedAt).absoluteValue
        require(off <= MAX_AGE_MS) { "A clip was sealed ${off / 1000}s away from now: a replay, or a clock is wrong" }
        opened.add(sealedAt, raw.copyOfRange(1, 1 + NONCE_LEN), now)
        return String(text, STAMP_LEN, text.size - STAMP_LEN, Charsets.UTF_8)
    }

    /** The clips opened lately, so that none opens twice. */
    class Opened {
        private val clips = ArrayList<Pair<Long, ByteArray>>()

        internal fun add(sealedAt: Long, nonce: ByteArray, now: Long) {
            // Older entries can't match: their clips fail the time check.
            clips.removeAll { (at, _) -> (now - at).absoluteValue > MAX_AGE_MS }
            require(clips.none { (_, seen) -> seen.contentEquals(nonce) }) { "A clip came twice: a replay" }
            clips.add(sealedAt to nonce)
        }
    }

    private companion object {
        const val SECRET_LEN = 32
        const val NONCE_LEN = 12
        const val TAG_BITS = 128
        const val VERSION: Byte = 2
        /** The sealing time's length: milliseconds since the Unix epoch, big-endian. */
        const val STAMP_LEN = 8
        /**
         * How far a clip's sealing time may be from this phone's clock. It covers how long the relay
         * keeps a clip (`CLIP_TTL_SECS`, 2 minutes by default) and clocks that drift apart.
         */
        const val MAX_AGE_MS = 5 * 60 * 1000L
        const val CIPHER = "AES/GCM/NoPadding"
        val ENCODER: Base64.Encoder = Base64.getUrlEncoder().withoutPadding()
        val DECODER: Base64.Decoder = Base64.getUrlDecoder()
        val RANDOM = SecureRandom()

        fun hmac(key: ByteArray, label: String): ByteArray =
            Mac.getInstance("HmacSHA256").run {
                init(SecretKeySpec(key, "HmacSHA256"))
                doFinal(label.toByteArray(Charsets.UTF_8))
            }
    }
}
