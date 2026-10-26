package com.nightclip.app.core

import com.nightclip.app.logic.EntryDraft
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.ZoneId

class VaultApiJsonTest {
    private val utc = ZoneId.of("UTC")

    @Test
    fun entryJsonHasTheCoreShape() {
        val d = EntryDraft(
            kind = "apikey", title = "  OpenAI ", password = "sk-1", url = " https://api.example.com ", notes = "n",
            tags = "ai, prod ai", totp = " ", expires = "2027-01-31", groupId = "g1",
        ).withField("Key ID", "AK1").withField(" Region ", "eu", protected = true)
        val o = VaultApi.entryJson(d, utc)
        assertEquals("apikey", o.getString("kind"))
        assertEquals("OpenAI", o.getString("title"))
        assertEquals("https://api.example.com", o.getString("url"))
        assertEquals("sk-1", o.getString("password"))
        assertEquals("", o.getString("totp"))
        assertEquals("g1", o.getString("group_id"))
        assertEquals(listOf("ai", "prod"), (0 until o.getJSONArray("tags").length()).map { o.getJSONArray("tags").getString(it) })
        val f = o.getJSONArray("fields")
        assertEquals(2, f.length())
        assertEquals("Region", f.getJSONObject(1).getString("name"))
        assertTrue(f.getJSONObject(1).getBoolean("protected"))
        assertFalse(f.getJSONObject(0).getBoolean("protected"))
        assertEquals(1801439999L, o.getLong("expires_at")) // 2027-01-31 23:59:59 UTC
    }

    @Test
    fun noExpiryIsZero() {
        assertEquals(0L, VaultApi.entryJson(EntryDraft(title = "x"), utc).getLong("expires_at"))
    }

    @Test
    fun credentialsJsonUsesBase64KeyFile() {
        val o = JSONObject()
        VaultApi.putCreds(o, VaultCreds("pw", byteArrayOf(1, 2, 3)))
        assertEquals("pw", o.getString("password"))
        assertEquals("AQID", o.getString("key_file_b64"))
        val c = JSONObject()
        VaultApi.putCreds(c, VaultCreds("x"), "confirm_password", "confirm_key_file_b64")
        assertEquals("x", c.getString("confirm_password"))
        assertFalse(c.has("confirm_key_file_b64"))
    }
}
