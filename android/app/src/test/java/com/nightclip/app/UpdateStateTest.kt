package com.nightclip.app

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdateStateTest {
    private fun parse(json: String) = UpdateState.from(JSONObject(json))

    @Test
    fun parsesTheCoreStatusShape() {
        val u = parse(
            """{"enabled":true,"auto":false,"state":"downloading","current_code":5,"current_name":"0.1.5",
               "latest":{"version_code":9,"version_name":"0.1.9","size":1000,"notes":"fixes"},
               "mandatory":true,"received":250,"total":1000}""",
        )
        assertTrue(u.enabled)
        assertFalse(u.auto)
        assertEquals(9L, u.latestCode)
        assertEquals("0.1.9", u.latestName)
        assertEquals("fixes", u.notes)
        assertTrue(u.mandatory)
        assertEquals(0.25f, u.progress, 0.001f)
        assertEquals(UpdateState.Action.NONE, u.action)
        assertTrue(u.text.contains("25%"))
    }

    @Test
    fun buttonFollowsState() {
        val expected = mapOf(
            "idle" to UpdateState.Action.CHECK,
            "up_to_date" to UpdateState.Action.CHECK,
            "error" to UpdateState.Action.CHECK,
            "available" to UpdateState.Action.DOWNLOAD,
            "ready" to UpdateState.Action.INSTALL,
            "checking" to UpdateState.Action.NONE,
            "downloading" to UpdateState.Action.NONE,
        )
        for ((state, action) in expected) {
            val u = parse("""{"enabled":true,"state":"$state","current_code":5}""")
            assertEquals(state, action, u.action)
            assertEquals(state, action == UpdateState.Action.NONE, u.buttonText.isEmpty())
        }
    }

    @Test
    fun disabledBuildsExplainThemselvesAndOfferNoAction() {
        val dev = parse("""{"enabled":false,"state":"idle","current_code":0}""")
        assertEquals(UpdateState.Action.NONE, dev.action)
        assertTrue(dev.text.contains("Development"))
        assertTrue(parse("""{"enabled":false,"current_code":7}""").text.contains("not configured"))
    }

    @Test
    fun errorTextPrefersTheMessage() {
        val u = parse("""{"enabled":true,"state":"error","error":{"code":"UPDATE_FAILED","message":"HTTP 503"}}""")
        assertTrue(u.text.contains("HTTP 503"))
        val codeOnly = parse("""{"enabled":true,"state":"error","error":{"code":"UPDATE_FAILED","message":""}}""")
        assertTrue(codeOnly.text.contains("UPDATE_FAILED"))
    }

    @Test
    fun toleratesMissingFieldsAndClampsProgress() {
        val empty = parse("{}")
        assertFalse(empty.enabled)
        assertEquals(UpdateState.Action.NONE, empty.action)
        assertEquals(0f, empty.progress, 0f)
        assertEquals(1f, parse("""{"enabled":true,"state":"downloading","received":2000,"total":1000}""").progress, 0f)
    }

    @Test
    fun requiredUpdatesAreMarked() {
        assertTrue(parse("""{"enabled":true,"state":"available","latest":{"version_name":"2"},"mandatory":true}""").text.contains("required"))
        assertFalse(parse("""{"enabled":true,"state":"available","latest":{"version_name":"2"}}""").text.contains("required"))
    }
}
