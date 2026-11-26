package dev.iyazerski.clipsync

import android.content.Context
import androidx.annotation.StringRes
import java.io.IOException

/** Words for the user, kept as a string resource and its arguments so they read in the current system language. */
data class Message(@StringRes val id: Int, val args: List<Any> = emptyList()) {
    fun read(context: Context): String = context.getString(id, *args.toTypedArray())
}

/** Input the user can fix, and what's wrong with it. */
class Invalid(val reason: Message) : IllegalArgumentException() {
    constructor(@StringRes id: Int, vararg args: Any) : this(Message(id, args.toList()))
}

/** A failure the app can explain to the user. */
open class Failed(val reason: Message) : IOException() {
    constructor(@StringRes id: Int, vararg args: Any) : this(Message(id, args.toList()))
}

/** Why [e] happened: in the user's language when the app has the words, else as the system put it. */
fun Context.reason(e: IOException): String = if (e is Failed) e.reason.read(this) else e.message.toString()
