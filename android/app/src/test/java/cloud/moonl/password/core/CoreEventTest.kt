package cloud.moonl.password.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class CoreEventTest {
    private inline fun <reified T : CoreEvent> parse(json: String): T {
        val ev = CoreEvent.parse(json)
        assertTrue("parsed: $ev", ev is T)
        return ev as T
    }

    @Test
    fun updateEvents() {
        val a = parse<CoreEvent.UpdateAvailable>("""{"type":"update.available","version_code":12,"version_name":"0.1.12","mandatory":true,"notes":"n"}""")
        assertEquals(12L, a.versionCode)
        assertTrue(a.mandatory)
        assertEquals(CoreEvent.UpdateProgress(10, 100), parse<CoreEvent.UpdateProgress>("""{"type":"update.progress","received":10,"total":100}"""))
        assertEquals("/p.apk", parse<CoreEvent.UpdateReady>("""{"type":"update.ready","version_code":1,"version_name":"v","path":"/p.apk","mandatory":false}""").path)
        assertEquals("HASH_MISMATCH", parse<CoreEvent.UpdateError>("""{"type":"update.error","code":"HASH_MISMATCH","message":"m"}""").code)
    }

    @Test
    fun vaultEvents() {
        assertEquals("idle", parse<CoreEvent.VaultLocked>("""{"type":"vault.locked","reason":"idle"}""").reason)
        assertEquals("sync", parse<CoreEvent.VaultChanged>("""{"type":"vault.changed","reason":"sync"}""").reason)
    }

    @Test
    fun coreStarted() {
        assertEquals(CoreEvent.CoreStarted, CoreEvent.parse("""{"type":"core.started"}"""))
    }

    @Test
    fun retiredUnknownAndMalformedEvents() {
        assertEquals("future.thing", parse<CoreEvent.Unknown>("""{"type":"future.thing","x":1}""").type)
        // events of the removed clipboard / pairing features are no longer typed
        assertEquals("clip.received", parse<CoreEvent.Unknown>("""{"type":"clip.received","event_id":"e1","text":"x"}""").type)
        assertEquals("pair.sas", parse<CoreEvent.Unknown>("""{"type":"pair.sas","join_id":"j1","sas":"1"}""").type)
        assertNull(CoreEvent.parse("not json"))
        assertNull(CoreEvent.parse("""{"no":"type"}"""))
        assertNull(CoreEvent.parse(""))
    }
}
