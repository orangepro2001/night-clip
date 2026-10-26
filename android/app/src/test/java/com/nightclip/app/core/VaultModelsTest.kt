package com.nightclip.app.core

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class VaultModelsTest {
    @Test
    fun statusLockedWithRetry() {
        val s = VaultParsers.status(JSONObject("""{"exists":true,"unlocked":false,"autolock_s":300,"clip_clear_s":20,"retry_after_s":12}"""))
        assertTrue(s.exists)
        assertFalse(s.unlocked)
        assertEquals(12, s.retryAfterS)
        assertNull(s.entries)
        assertNull(s.kdf)
        assertEquals(0, s.backups)
    }

    @Test
    fun statusUnlockedWithKdfAndBackups() {
        val s = VaultParsers.status(
            JSONObject(
                """{"exists":true,"unlocked":true,"autolock_s":120,"clip_clear_s":0,"retry_after_s":0,
                    "entries":7,"kdf":{"MemoryMiB":64,"Iterations":3,"Parallelism":2},"backups":5,"last_manual_backup_at":1790000000}""",
            ),
        )
        assertTrue(s.unlocked)
        assertEquals(7, s.entries)
        assertEquals(VaultKdfInfo(64, 3, 2), s.kdf)
        assertEquals(5, s.backups)
        assertEquals(1790000000L, s.lastManualBackupAt)
        assertEquals(0, s.clipClearS)
    }

    @Test
    fun statusMissingKeysFallBackToDefaults() {
        val s = VaultParsers.status(JSONObject("{}"))
        assertFalse(s.exists)
        assertEquals(300, s.autolockS)
        assertEquals(20, s.clipClearS)
    }

    @Test
    fun listParsesSummariesWithoutSecrets() {
        val list = VaultParsers.list(
            JSONObject(
                """{"entries":[
                {"id":"a1","group_id":"g","kind":"apikey","title":"OpenAI","username":"","url":"https://api.openai.com","tags":["ai","prod"],
                 "has_password":true,"has_totp":false,"has_attachments":true,"modified_at":10,"expires_at":99,"in_trash":false},
                {"id":"b2","kind":"login","title":"Bank","has_totp":true,"in_trash":true}]}""",
            ),
        )
        assertEquals(2, list.size)
        assertEquals("apikey", list[0].kind)
        assertEquals(listOf("ai", "prod"), list[0].tags)
        assertTrue(list[0].hasPassword)
        assertTrue(list[0].hasAttachments)
        assertEquals(99L, list[0].expiresAt)
        assertTrue(list[1].hasTotp)
        assertTrue(list[1].inTrash)
        assertEquals(emptyList<String>(), list[1].tags)
    }

    @Test
    fun nullEntriesIsEmptyList() {
        assertTrue(VaultParsers.list(JSONObject("""{"entries":null}""")).isEmpty())
        assertTrue(VaultParsers.list(JSONObject("{}")).isEmpty())
    }

    @Test
    fun detailKeepsProtectedValuesEmpty() {
        val d = VaultParsers.detail(
            JSONObject(
                """{"id":"x","group_id":"g1","kind":"apikey","title":"T","username":"u","url":"https://e","notes":"n","tags":["t"],
                "fields":[{"name":"Key ID","value":"AK1","protected":false,"has_value":true},{"name":"Secret","value":"","protected":true,"has_value":true}],
                "has_password":true,"has_totp":false,"expires_at":5,"created_at":1,"modified_at":2,
                "attachments":[{"name":"a.txt","size":12}],"history":3,"in_trash":false,"password_strength":{"bits":71.5,"label":"good"}}""",
            ),
        )
        assertEquals(2, d.fields.size)
        assertEquals("AK1", d.fields[0].value)
        assertTrue(d.fields[1].protected)
        assertEquals("", d.fields[1].value)
        assertTrue(d.fields[1].hasValue)
        assertEquals(listOf(VaultAttachment("a.txt", 12)), d.attachments)
        assertEquals(3, d.historyCount)
        assertEquals("good", d.strength.label)
        assertEquals(71.5, d.strength.bits, 0.001)
    }

    @Test
    fun historyGroupsAndHealth() {
        val h = VaultParsers.history(JSONObject("""{"versions":[{"index":0,"modified_at":5,"title":"Old","username":"u","url":""}]}"""))
        assertEquals(1, h.size)
        assertEquals("Old", h[0].title)
        val g = VaultParsers.groups(JSONObject("""{"groups":[{"id":"r","parent_id":"","name":"Root","path":"","entries":3,"is_trash":false},{"id":"t","parent_id":"r","name":"回收站","path":"回收站","entries":1,"is_trash":true}]}"""))
        assertTrue(g[1].isTrash)
        val hl = VaultParsers.health(JSONObject("""{"weak":[{"id":"1","title":"A","info":"weak"}],"reused":[{"id":"2","title":"B","info":"2"}],"old":[],"expired":[{"id":"3","title":"C"}],"without_totp":4,"total":9}"""))
        assertEquals(3, hl.issueCount)
        assertEquals(4, hl.withoutTotp)
        assertEquals("", hl.expired[0].info)
    }

    @Test
    fun backupsPreviewAndStats() {
        val b = VaultParsers.backups(JSONObject("""{"backups":[{"name":"vault-1.kdbx","time":100,"size":2048,"reason":"rekey"},{"name":"vault.kdbx.prev","time":90,"size":2000,"reason":"previous"}]}"""))
        assertEquals("rekey", b[0].reason)
        assertEquals(2, b.size)
        val c = VaultParsers.backupCheck(JSONObject("""{"entries":4,"groups":2,"in_trash":1,"modified_at":77}"""))
        assertEquals(BackupCheck(4, 2, 1, 77), c)
        val p = VaultParsers.restorePreview(
            JSONObject("""{"only_in_backup":[{"id":"a","title":"A","modified_at":1}],"only_in_current":[],"changed":[{"id":"b","title":"B","modified_at":2}],"same":6}"""),
        )
        assertEquals(1, p.onlyInBackup.size)
        assertEquals(6, p.same)
        val s = VaultParsers.stats(JSONObject("""{"stats":{"entries_added":2,"entries_updated":1,"groups_added":1}}"""))
        assertEquals(MergeStats(entriesAdded = 2, entriesUpdated = 1, groupsAdded = 1), s)
        assertEquals(CsvImportResult(3, 1), VaultParsers.csvResult(JSONObject("""{"added":3,"skipped":1}""")))
    }

    @Test
    fun totpCopyGenerated() {
        assertEquals(TotpCode("123456", 17), VaultParsers.totp(JSONObject("""{"code":"123456","seconds_left":17}""")))
        val c = VaultParsers.copy(JSONObject("""{"value":"s3cret","clear_after_s":20}"""))
        assertEquals(20, c.clearAfterS)
        assertFalse(c.toString().contains("s3cret"))
        val g = VaultParsers.generated(JSONObject("""{"value":"abc","strength":{"bits":10.0,"label":"weak"}}"""))
        assertEquals("weak", g.strength.label)
        assertFalse(g.toString().contains("abc"))
        assertEquals("strong", VaultParsers.strength(JSONObject("""{"bits":99,"label":"strong"}""")).label)
    }

    @Test
    fun credsNeverPrintSecrets() {
        assertFalse(VaultCreds("hunter2", byteArrayOf(1, 2)).toString().contains("hunter2"))
    }

    @Test
    fun replyCarriesRetryAfterOfRateLimited() {
        val r = Reply.parse("""{"ok":false,"error":{"code":"RATE_LIMITED","message":"slow down","retryable":true,"details":{"retry_after_s":8}}}""")
        assertFalse(r.ok)
        assertEquals("RATE_LIMITED", r.errorCode)
        assertEquals(8, r.retryAfterS)
        assertEquals(0, Reply.parse("""{"ok":false,"error":{"code":"VAULT_LOCKED","message":"x"}}""").retryAfterS)
    }

    @Test
    fun vaultLockedEventParses() {
        val ev = CoreEvent.parse("""{"type":"vault.locked","reason":"idle"}""")
        assertEquals(CoreEvent.VaultLocked("idle"), ev)
        assertEquals("vault.locked", ev?.type)
    }
}
