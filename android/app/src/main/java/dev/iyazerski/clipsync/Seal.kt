package dev.iyazerski.clipsync

import java.security.GeneralSecurityException
import java.security.SecureRandom
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.Mac
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

/**
 * Sealing clips, so the relay passes them on without reading them. The same scheme as the Mac's
 * `src/seal.rs`, which shares its test vectors with SealTest.
 *
 * One secret, shared by your devices, gives the relay token and the sealing key: each is an
 * HMAC-SHA256 of a label, keyed with the secret. A sealed clip is AES-256-GCM,
 * `version || nonce || ciphertext || tag`, in base64url without padding.
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

    /** Seal [text] with a fresh random nonce. */
    fun seal(text: String): String = sealWith(ByteArray(NONCE_LEN).also(RANDOM::nextBytes), text)

    internal fun sealWith(nonce: ByteArray, text: String): String {
        val cipher = Cipher.getInstance(CIPHER)
        cipher.init(Cipher.ENCRYPT_MODE, key, GCMParameterSpec(TAG_BITS, nonce))
        val body = cipher.doFinal(text.toByteArray(Charsets.UTF_8))
        return ENCODER.encodeToString(byteArrayOf(VERSION) + nonce + body)
    }

    /** Open a sealed clip; throws if it was sealed with another secret or changed. */
    fun open(sealed: String): String {
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
        return String(text, Charsets.UTF_8)
    }

    private companion object {
        const val SECRET_LEN = 32
        const val NONCE_LEN = 12
        const val TAG_BITS = 128
        const val VERSION: Byte = 1
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
