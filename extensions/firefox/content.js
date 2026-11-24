// Executed in the isolated world of the top-level document after a toolbar action.
if (!globalThis.moonPasswordForm) {
  const token = crypto.randomUUID();
  const setValue = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set;

  const usable = (input) => !input.disabled && !input.readOnly &&
    input.getClientRects().length > 0 && getComputedStyle(input).visibility === "visible";
  const hint = (input, type) => input.autocomplete.toLowerCase().split(/\s+/).includes(type);
  const label = (input) => [input.id, input.name, input.getAttribute("aria-label")].join(" ");
  const current = (input) => hint(input, "current-password") || /old|current/i.test(label(input));
  const fresh = (input) => !current(input) &&
    (hint(input, "new-password") || /new|confirm|signup|register|repeat/i.test(label(input)));
  const sameOriginForm = (form) => {
    try {
      const action = form.getAttribute("action");
      if (action && new URL(action, document.baseURI).origin !== location.origin) return false;
      // A submit control can override the form destination.
      return [...form.elements].every((element) => {
        const target = element.getAttribute("formaction");
        return target === null || new URL(target || location.href, document.baseURI).origin === location.origin;
      });
    } catch { return false; }
  };

  function candidates(kind) {
    return [...document.forms].filter(sameOriginForm).flatMap((form) => {
      const fields = [...form.elements].filter((element) => element instanceof HTMLInputElement && usable(element));
      const passwords = fields.filter((element) => element.type === "password");
      let targets;
      if (kind === "new") {
        targets = passwords.filter(fresh);
        if (targets.length === 1 && passwords.length === 2 && !passwords.some(current)) targets = passwords;
        if (targets.length < 1 || targets.length > 2) return [];
      } else {
        targets = passwords.filter(current);
        if (!targets.length && passwords.length === 1 && !fresh(passwords[0])) targets = passwords;
        if (targets.length !== 1) return [];
      }
      const texts = fields.filter((element) => ["text", "email", "tel"].includes(element.type) && !hint(element, "one-time-code"));
      const user = texts.find((element) => hint(element, "username")) ??
        texts.find((element) => element.type === "email" || hint(element, "email")) ??
        texts.find((element) => /user(name)?|login|account/i.test(label(element)));
      return [{ form, user, targets }];
    });
  }

  function pick(kind) {
    const choices = candidates(kind);
    if (choices.length === 1) return choices[0];
    return choices.find(({ form }) => form.contains(document.activeElement));
  }

  function valid(expected, kind, chosen) {
    if (expected.token !== token || expected.href !== location.href || !chosen) return false;
    return candidates(kind).some((entry) => entry.form === chosen.form && entry.user === chosen.user &&
      entry.targets.length === chosen.targets.length && entry.targets.every((input, index) => input === chosen.targets[index]));
  }

  let login, signup;
  globalThis.moonPasswordForm = {
    inspect() {
      login = pick("login");
      signup = pick("new");
      return { token, href: location.href, login: Boolean(login), signup: Boolean(signup) };
    },
    username(expected) {
      if (!valid(expected, "new", signup)) return null;
      return signup.user?.value ?? "";
    },
    apply(expected, kind, { username, password }) {
      const chosen = kind === "new" ? signup : login;
      if (!valid(expected, kind, chosen)) return false;
      if (kind !== "new" && username && chosen.user) {
        setValue.call(chosen.user, username);
        chosen.user.dispatchEvent(new Event("input", { bubbles: true }));
        chosen.user.dispatchEvent(new Event("change", { bubbles: true }));
      }
      for (const input of chosen.targets) {
        if (!valid(expected, kind, chosen)) return false;
        setValue.call(input, password);
        input.dispatchEvent(new Event("input", { bubbles: true }));
        input.dispatchEvent(new Event("change", { bubbles: true }));
      }
      return true;
    },
  };
}
