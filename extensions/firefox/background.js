const hostName = "cloud.moonl.moonpassword";
const last = new Map();
const running = new Set();
const popupUrl = browser.runtime.getURL("popup.html");

const fail = (error) => ({ ok: false, error });

function targetOrigin(raw) {
  try {
    const url = new URL(raw);
    if (url.username || url.password) return null;
    if (url.protocol === "https:") return url.origin;
    if (url.protocol === "http:" && (url.hostname === "localhost" || url.hostname === "[::1]" ||
        /^127\.\d+\.\d+\.\d+$/.test(url.hostname))) return url.origin;
  } catch { /* invalid URL */ }
  return null;
}

async function native(request) {
  try { return await browser.runtime.sendNativeMessage(hostName, request); }
  catch { return fail("APP_UNAVAILABLE"); }
}

async function script(tabId, operation, args = []) {
  const [result] = await browser.scripting.executeScript({
    target: { tabId },
    func: (method, parameters) => globalThis.moonPasswordForm?.[method](...parameters),
    args: [operation, args],
  });
  return result?.result;
}

async function activeTab() {
  const [tab] = await browser.tabs.query({ active: true, currentWindow: true });
  return tab;
}

async function inspect(tab) {
  const origin = targetOrigin(tab?.url);
  if (!origin || !Number.isInteger(tab.id)) return fail("INVALID_ORIGIN");
  if (running.has(tab.id)) return fail("BUSY");
  try {
    await browser.scripting.executeScript({ target: { tabId: tab.id }, files: ["content.js"] });
    const form = await script(tab.id, "inspect");
    if (!form || form.href !== tab.url) return fail("PAGE_CHANGED");
    const previous = last.get(tab.id);
    const match = form.login && !running.has(tab.id) ? await native({ action: "match", origin }) : null;
    return { ok: true, form, tabId: tab.id, origin, match,
      busy: running.has(tab.id), previous: previous?.href === form.href ? previous.outcome : null };
  } catch { return fail("UNAVAILABLE_PAGE"); }
}

function generatePassword() {
  const groups = ["ABCDEFGHJKLMNPQRSTUVWXYZ", "abcdefghijkmnopqrstuvwxyz", "23456789", "!#$%&*+-=?@"];
  const alphabet = groups.join("");
  const bytes = new Uint8Array(32);
  const limit = 256 - (256 % alphabet.length);
  for (;;) {
    let password = "";
    while (password.length < 24) {
      crypto.getRandomValues(bytes);
      for (const value of bytes) {
        if (value < limit) password += alphabet[value % alphabet.length];
        if (password.length === 24) break;
      }
    }
    if (groups.every((group) => [...password].some((c) => group.includes(c)))) return password;
  }
}

async function perform(message) {
  const { tabId, form } = message;
  const tab = await activeTab();
  const origin = targetOrigin(tab?.url);
  if (!origin || tab.id !== tabId || tab.url !== form?.href) return fail("PAGE_CHANGED");
  if (running.has(tabId)) return fail("BUSY");
  running.add(tabId);
  let outcome = fail("UNAVAILABLE_PAGE");
  try {
    await browser.action.setBadgeText({ tabId, text: "…" });
    let values;
    let kind;
    if (message.action === "fill") {
      kind = "login";
      values = await native({ action: "fill", origin, id: message.id });
    } else {
      kind = "new";
      if (!form.signup) return outcome = fail("NO_FORM");
      const username = await script(tabId, "username", [form]);
      if (typeof username !== "string") return outcome = fail("PAGE_CHANGED");
      const password = generatePassword();
      if (message.action === "generate-save") {
        const saved = await native({ action: "save", origin, username, password });
        if (!saved.ok) return outcome = saved;
      }
      values = { ok: true, username, password };
    }
    if (!values.ok) return outcome = values;
    const still = await browser.tabs.get(tabId);
    if (!still.active || still.url !== form.href || !await script(tabId, "apply", [form, kind, values])) {
      return outcome = fail(message.action === "generate-save" ? "SAVED_PAGE_CHANGED" : "PAGE_CHANGED");
    }
    return outcome = { ok: true, action: message.action };
  } catch {
    return outcome;
  } finally {
    running.delete(tabId);
    last.set(tabId, { href: form.href, outcome });
    try { await browser.action.setBadgeText({ tabId, text: outcome.ok ? "✓" : "!" }); }
    catch { /* tab closed */ last.delete(tabId); }
  }
}

browser.tabs.onRemoved.addListener((id) => last.delete(id));
browser.runtime.onMessage.addListener((message, sender) => {
  if (sender.url !== popupUrl || !message) return undefined;
  switch (message.action) {
    case "inspect": return activeTab().then(inspect);
    case "open": return native({ action: "open" });
    case "fill":
    case "generate":
    case "generate-save": return perform(message);
    default: return Promise.resolve(fail("UNKNOWN_ACTION"));
  }
});
