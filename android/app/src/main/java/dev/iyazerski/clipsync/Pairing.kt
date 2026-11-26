package dev.iyazerski.clipsync

import org.json.JSONException
import org.json.JSONObject
import java.math.BigInteger
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL
import java.security.GeneralSecurityException
import java.security.KeyFactory
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.spec.PKCS8EncodedKeySpec
import java.security.spec.X509EncodedKeySpec
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.KeyAgreement
import javax.crypto.Mac
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

/**
 * CPace-X25519-SHA512 from draft-irtf-cfrg-cpace-21, as `internal/client/cpace.go` does it. It turns
 * a short code into a key that both devices share, and lets nobody test more than one guess per code.
 */
internal object CPace {
    private const val DSI = "CPace255"
    private val P: BigInteger = BigInteger.ONE.shiftLeft(255) - BigInteger.valueOf(19)
    private val A = BigInteger.valueOf(486662)
    // DER headers of X25519 keys, in front of their 32 raw bytes.
    private val PKCS8 = hex("302e020100300506032b656e04220420")
    private val X509 = hex("302a300506032b656e032100")

    fun prependLen(data: ByteArray): ByteArray {
        val out = java.io.ByteArrayOutputStream()
        var n = data.size
        while (true) {
            out.write(if (n < 128) n else (n and 0x7f) or 0x80)
            n = n shr 7
            if (n == 0) break
        }
        out.write(data)
        return out.toByteArray()
    }

    fun lvCat(vararg parts: ByteArray): ByteArray = parts.fold(ByteArray(0)) { out, p -> out + prependLen(p) }

    /** A point from the code [prs], channel identifier [ci] and session id [sid]: nobody knows its discrete log. */
    fun generator(prs: ByteArray, ci: ByteArray, sid: ByteArray): ByteArray {
        val dsi = DSI.toByteArray()
        val zpad = maxOf(0, 128 - 1 - prependLen(prs).size - prependLen(dsi).size)
        return elligator2(sha512(lvCat(dsi, prs, ByteArray(zpad), ci, sid)).copyOf(32))
    }

    /** Maps a little-endian field element to the u-coordinate of a point on Curve25519. */
    private fun elligator2(r: ByteArray): ByteArray {
        val le = r.copyOf().also { it[31] = (it[31].toInt() and 0x7f).toByte() }
        val u = BigInteger(1, le.reversedArray())
        // v = -A / (1 + 2u²), where 2 is the non-square that RFC 9380 picks for Curve25519.
        val v = A.negate().multiply((BigInteger.ONE + u * u * BigInteger.TWO).modInverse(P)).mod(P)
        // ε = (v³ + Av² + v)^((p-1)/2) is 1 when v is a point's u-coordinate, else -1 (or 0).
        val eps = (v * v * v + A * v * v + v).mod(P).modPow((P - BigInteger.ONE).shiftRight(1), P)
        // x = εv - (1-ε)A/2
        val x = (eps * v - (BigInteger.ONE - eps).mod(P) * A * BigInteger.TWO.modInverse(P)).mod(P)
        val be = x.toByteArray().takeLast(32).toByteArray()
        return (ByteArray(32 - be.size) + be).reversedArray()
    }

    /** One side's secret scalar and the share it sends: the generator times the scalar. */
    class Share(generator: ByteArray, private val scalar: ByteArray = random(32)) {
        val public: ByteArray = x25519(scalar, generator)

        /** The intermediate session key, from the other side's share and both in the initiator's order. */
        fun isk(sid: ByteArray, peer: ByteArray, ya: ByteArray, ada: ByteArray, yb: ByteArray, adb: ByteArray): ByteArray =
            sha512(lvCat("${DSI}_ISK".toByteArray(), sid, x25519(scalar, peer)) + lvCat(ya, ada) + lvCat(yb, adb))
    }

    /** Throws [GeneralSecurityException] for low-order points, whose product is the identity. */
    private fun x25519(scalar: ByteArray, point: ByteArray): ByteArray {
        val keys = KeyFactory.getInstance("XDH")
        return KeyAgreement.getInstance("XDH").run {
            init(keys.generatePrivate(PKCS8EncodedKeySpec(PKCS8 + scalar)))
            doPhase(keys.generatePublic(X509EncodedKeySpec(X509 + point)), true)
            generateSecret()
        }
    }

    fun sha512(data: ByteArray): ByteArray = MessageDigest.getInstance("SHA-512").digest(data)

    fun random(size: Int) = ByteArray(size).also(SecureRandom()::nextBytes)

    private fun hex(s: String) = ByteArray(s.length / 2) { s.substring(it * 2, it * 2 + 2).toInt(16).toByte() }
}

/**
 * Joining the computers by the code that one of them shows: its relay mailbox's nameplate and 6
 * secret digits, as `internal/client/pairing.go` does it.
 */
object CodePairing {
    class Pairing(val relay: String, val secret: String)

    /** The code has expired, was used, or never was. */
    class Gone : Failed(R.string.code_gone)

    /** The computer has another code. */
    class Wrong : Failed(R.string.code_wrong)

    private const val PATH = "/pairing"
    private const val TIMEOUT_MS = 15_000
    private const val DIGITS = 6
    private const val SID = 16
    private const val SHARE = 32
    private const val TAG = 64
    private const val NONCE = 12
    private val CI = "clipsync pairing".toByteArray()
    private val AD_INVITER = "inviter".toByteArray()
    private val AD_JOINER = "joiner".toByteArray()
    private val ENCODER: Base64.Encoder = Base64.getUrlEncoder().withoutPadding()
    private val DECODER: Base64.Decoder = Base64.getUrlDecoder()

    /** A code as people type it, with spaces or dashes and in any script's digits, or null. */
    fun parse(text: String): String? {
        val code = StringBuilder()
        for (c in text.trim().codePoints()) {
            when {
                c == ' '.code || c == '-'.code || c == '\u00a0'.code -> {}
                Character.isDigit(c) -> code.append(Character.getNumericValue(c))
                else -> return null
            }
        }
        return code.toString().takeIf { it.length in DIGITS + 2..DIGITS + 4 }
    }

    /** The relay and secret in a pairing link, `https://relay/pair#secret`, or null. */
    fun parseLink(text: String): Pairing? {
        val link = runCatching { URI(text.trim()) }.getOrNull() ?: return null
        val secret = link.rawFragment?.takeIf { it.isNotEmpty() } ?: return null
        val path = link.rawPath.orEmpty()
        if (link.scheme == null || link.rawAuthority == null || !path.endsWith("/pair")) return null
        return Pairing("${link.scheme}://${link.rawAuthority}${path.removeSuffix("/pair")}", secret)
    }

    /** Pair with the computer that shows [code], through [relay]. Blocks on the network. */
    fun join(relay: String, code: String): Pairing {
        val address = relay.trim().trimEnd('/')
        val nameplate = code.dropLast(DIGITS)
        val password = code.takeLast(DIGITS).toByteArray()
        val opening = read(address, nameplate, "a")
        if (opening.size != SID + SHARE) throw Wrong()
        val sid = opening.copyOf(SID)
        val ya = opening.copyOfRange(SID, opening.size)
        val isk: ByteArray
        val share: CPace.Share
        try {
            share = CPace.Share(CPace.generator(password, CI, sid))
            isk = share.isk(sid, ya, ya, AD_INVITER, share.public, AD_JOINER)
        } catch (e: GeneralSecurityException) {
            throw Wrong()
        }
        val key = CPace.sha512("CPaceMac".toByteArray() + sid + isk)
        write(address, nameplate, "b", share.public + tag(key, share.public, AD_JOINER))
        // The computer closes the pairing when the codes don't match.
        val reply = try {
            read(address, nameplate, "c")
        } catch (e: Gone) {
            throw Wrong()
        }
        if (reply.size < TAG + NONCE || !MessageDigest.isEqual(reply.copyOf(TAG), tag(key, ya, AD_INVITER))) throw Wrong()
        val plain = try {
            Cipher.getInstance("AES/GCM/NoPadding").run {
                val aes = Mac.getInstance("HmacSHA512").run {
                    init(SecretKeySpec(isk, "HmacSHA512"))
                    doFinal("clipsync/pairing".toByteArray()).copyOf(32)
                }
                init(Cipher.DECRYPT_MODE, SecretKeySpec(aes, "AES"), GCMParameterSpec(128, reply, TAG, NONCE))
                doFinal(reply, TAG + NONCE, reply.size - TAG - NONCE)
            }
        } catch (e: GeneralSecurityException) {
            throw Wrong()
        }
        return try {
            JSONObject(String(plain, Charsets.UTF_8)).run { Pairing(getString("relay"), getString("secret")) }
        } catch (e: JSONException) {
            throw Wrong()
        }
    }

    private fun tag(key: ByteArray, share: ByteArray, ad: ByteArray): ByteArray =
        Mac.getInstance("HmacSHA512").run {
            init(SecretKeySpec(key, "HmacSHA512"))
            doFinal(CPace.lvCat(share, ad))
        }

    private fun open(address: String, nameplate: String, slot: String): HttpURLConnection {
        if (nameplate.any { it !in '0'..'9' }) throw Gone()
        return (URL("$address$PATH/$nameplate/$slot").openConnection() as HttpURLConnection).apply {
            connectTimeout = TIMEOUT_MS
            // The relay holds a read for up to 15 seconds.
            readTimeout = 2 * TIMEOUT_MS
            instanceFollowRedirects = false
        }
    }

    /** Wait for [slot]'s message. */
    private fun read(address: String, nameplate: String, slot: String): ByteArray {
        // The computer answers within its 10 minutes, or the relay forgets the code.
        val deadline = System.currentTimeMillis() + 11 * 60 * 1000L
        while (System.currentTimeMillis() < deadline) {
            val connection = open(address, nameplate, slot)
            try {
                when (val status = connection.responseCode) {
                    HttpURLConnection.HTTP_OK -> {
                        val body = connection.inputStream.use { it.readNBytes(2048) }
                        return runCatching { DECODER.decode(body) }.getOrNull() ?: throw Wrong()
                    }
                    HttpURLConnection.HTTP_NO_CONTENT -> {}
                    HttpURLConnection.HTTP_NOT_FOUND -> throw Gone()
                    else -> throw Failed(R.string.relay_answered, status)
                }
            } finally {
                connection.disconnect()
            }
        }
        throw Gone()
    }

    private fun write(address: String, nameplate: String, slot: String, message: ByteArray) {
        val connection = open(address, nameplate, slot)
        try {
            connection.requestMethod = "PUT"
            connection.doOutput = true
            connection.outputStream.use { it.write(ENCODER.encode(message)) }
            when (val status = connection.responseCode) {
                HttpURLConnection.HTTP_NO_CONTENT -> {}
                HttpURLConnection.HTTP_NOT_FOUND, HttpURLConnection.HTTP_CONFLICT -> throw Gone()
                else -> throw Failed(R.string.relay_answered, status)
            }
        } finally {
            connection.disconnect()
        }
    }
}
