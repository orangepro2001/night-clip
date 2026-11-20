package cloud.moonl.password.logic

import cloud.moonl.password.UpdateState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdatePresenterTest {
    private fun u(state: String, extra: String = "") =
        UpdateState.from(org.json.JSONObject("""{"enabled":true,"state":"$state","current_code":5,"current_name":"0.1.5","latest":{"version_name":"0.1.9"}$extra}"""))

    @Test
    fun textsAreChinese() {
        assertEquals("发现新版本 0.1.9", UpdatePresenter.text(u("available")))
        assertTrue(UpdatePresenter.text(u("available", ""","mandatory":true""")).contains("必须更新"))
        assertEquals("已是最新版本（0.1.5）", UpdatePresenter.text(u("up_to_date")))
        assertTrue(UpdatePresenter.text(u("downloading", ""","received":50,"total":100""")).contains("50%"))
        assertTrue(UpdatePresenter.text(UpdateState(enabled = false)).contains("开发版本"))
        assertEquals("检查更新", UpdatePresenter.buttonText(u("idle")))
        assertEquals("下载", UpdatePresenter.buttonText(u("available")))
        assertEquals("安装更新", UpdatePresenter.buttonText(u("ready")))
        assertEquals("", UpdatePresenter.buttonText(u("downloading")))
    }
}
