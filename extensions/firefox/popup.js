const status = document.getElementById("status");
const accounts = document.getElementById("accounts");
const errors = {
  APP_UNAVAILABLE: "请启动 Windows 版 MoonPassword，并在设置中启用 Firefox 本机连接。",
  DISABLED: "Firefox 本机连接已关闭。",
  VAULT_LOCKED: "请打开 Windows 版 MoonPassword 并解锁密码库。",
  INVALID_ORIGIN: "请在 HTTPS 登录页或本机登录页使用扩展。",
  UNAVAILABLE_PAGE: "Firefox 无法在这个页面识别登录表单。",
  PAGE_CHANGED: "页面或表单已变化，请重新打开扩展。",
  SAVED_PAGE_CHANGED: "密码已保存到 MoonPassword。页面已变化，请在密码库中找到新条目。",
  NO_MATCH: "账号已变更，请重新打开扩展。",
  CANCELLED: "操作已取消。",
  BUSY: "正在等待 MoonPassword 确认。",
};

function inform(message = "") {
  status.textContent = message;
  status.hidden = !message;
}

function outcome(result) {
  if (!result.ok) return errors[result.error] ?? "操作未完成，请在 MoonPassword 中确认密码库状态。";
  return ({ fill: "已填充账号。", generate: "已生成并填充新密码。", "generate-save": "已填充新密码并保存到密码库。" })[result.action] ?? "";
}

async function request(action, payload = {}) {
  try { return await browser.runtime.sendMessage({ action, ...payload }); }
  catch { return { ok: false, error: "UNAVAILABLE_PAGE" }; }
}

function makeAccount(entry, click) {
  const button = document.createElement("button");
  button.type = "button";
  const title = document.createElement("strong");
  title.textContent = entry.title;
  button.append(title);
  if (entry.username) {
    const username = document.createElement("span");
    username.textContent = entry.username;
    button.append(username);
  }
  button.addEventListener("click", click);
  return button;
}

async function load() {
  const page = await request("inspect");
  if (!page.ok) { inform(outcome(page)); return; }
  const site = document.getElementById("site");
  site.textContent = page.origin;
  site.hidden = false;
  if (page.busy) { inform(errors.BUSY); return; }
  const { form, tabId } = page;
  const perform = async (action, id) => {
    document.querySelectorAll("main button").forEach((button) => { button.disabled = true; });
    inform(action === "generate" ? "正在生成密码…" : "请在 Windows 版 MoonPassword 中确认操作。");
    const result = await request(action, { form, tabId, id });
    document.querySelectorAll("main button").forEach((button) => { button.disabled = false; });
    inform(outcome(result));
  };
  if (form.signup) {
    document.getElementById("signup").hidden = false;
    document.getElementById("generate").onclick = () => perform("generate");
    document.getElementById("generate-save").onclick = () => perform("generate-save");
  }
  if (page.match?.ok && page.match.matches.length) {
    document.getElementById("logins").hidden = false;
    accounts.replaceChildren(...page.match.matches.map((entry) => makeAccount(entry, () => perform("fill", entry.id))));
  }
  if (page.previous) inform(outcome(page.previous));
  else if (page.match && !page.match.ok) inform(outcome(page.match));
  else if (form.login && !page.match?.matches?.length) inform("这个网站没有已保存的账号。");
  else if (!form.login && !form.signup) inform("当前页面没有可填充的表单。有多个表单时，先点选其中一个密码框。");
  else inform();
}

document.getElementById("open").addEventListener("click", async () => {
  const response = await request("open");
  if (!response.ok) inform(outcome(response));
});
void load();
