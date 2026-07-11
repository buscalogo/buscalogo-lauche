(function () {
  const ID = "buscalogo-agent-chip";
  const PROXY = "/__buscalogo_agent__";

  function isBlHost() {
    return /\.bl$/i.test(location.hostname);
  }

  function ensureChip() {
    let el = document.getElementById(ID);
    if (el) return el;
    el = document.createElement("div");
    el.id = ID;
    el.setAttribute("role", "status");
    document.documentElement.appendChild(el);
    return el;
  }

  function hideChip() {
    const el = document.getElementById(ID);
    if (el) el.style.display = "none";
  }

  function render(result, url) {
    const el = ensureChip();
    el.className = "";
    if (result.skipped) {
      el.style.display = "none";
      return;
    }
    el.style.display = "flex";

    if (result.offline) {
      el.classList.add("bl-offline");
      el.innerHTML =
        '<span>Agente offline</span><a href="http://127.0.0.1:9970" target="_blank" rel="noopener">abrir</a>';
      return;
    }
    if (result.indexed) {
      el.classList.add("bl-ok");
      el.innerHTML = "<span>Indexada no BuscaLogo</span>";
      return;
    }
    el.classList.add("bl-suggest");
    el.innerHTML =
      '<span>Sugerir ao BuscaLogo</span><button type="button">Adicionar</button>';
    const btn = el.querySelector("button");
    btn.addEventListener("click", async (ev) => {
      ev.preventDefault();
      ev.stopPropagation();
      btn.disabled = true;
      btn.textContent = "…";
      try {
        if (isBlHost()) {
          const r = await fetch(PROXY + "/api/scraper/tasks", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              url,
              priority: "normal",
              type: "extension",
              discoveredFrom: "browser-extension",
            }),
          });
          const data = await r.json().catch(() => ({}));
          if (!r.ok || !data?.success) {
            throw new Error(data?.error || data?.message || "falha ao enfileirar");
          }
        } else {
          const res = await chrome.runtime.sendMessage({ type: "BL_SUGGEST", url });
          if (!res?.ok) throw new Error(res?.error || "falha");
        }
        el.className = "bl-ok";
        el.innerHTML = "<span>Enfileirada</span>";
      } catch (e) {
        btn.disabled = false;
        btn.textContent = "Adicionar";
        el.classList.add("bl-offline");
        el.querySelector("span").textContent = e.message || "Erro";
      }
    });
  }

  async function proxyFetch(path, method, body) {
    const opts = { method: method || "GET", headers: {} };
    if (method && method !== "GET" && method !== "HEAD") {
      opts.headers["Content-Type"] = "application/json";
      if (body) opts.body = typeof body === "string" ? body : JSON.stringify(body);
    }
    const r = await fetch(PROXY + path, opts);
    const data = await r.json().catch(() => ({}));
    return {
      ok: r.ok,
      status: r.status,
      data,
      base: location.origin + PROXY,
    };
  }

  async function lookupViaProxy(pageUrl) {
    try {
      const ver = await fetch(PROXY + "/api/version");
      if (!ver.ok) throw new Error("Agent offline");
      const q = encodeURIComponent(pageUrl);
      const r = await fetch(PROXY + "/api/scraper/lookup?url=" + q);
      const data = await r.json().catch(() => ({}));
      if (!r.ok || !data?.success) {
        return {
          offline: false,
          indexed: false,
          error: data?.error || `Lookup HTTP ${r.status}`,
        };
      }
      return { offline: false, indexed: !!data.data?.indexed, ...data.data };
    } catch (e) {
      return {
        offline: true,
        indexed: false,
        error: String(e.message || e),
      };
    }
  }

  chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
    if (msg?.type === "BL_LOOKUP") {
      render(msg.result || {}, msg.url || location.href);
      return;
    }
    if (msg?.type === "BL_HIDE_ALERT") {
      hideChip();
      return;
    }
    if (msg?.type === "BL_AGENT_FETCH") {
      if (!isBlHost()) {
        sendResponse({ ok: false, status: 0, error: "not a .bl page" });
        return;
      }
      proxyFetch(msg.path, msg.method, msg.body)
        .then(sendResponse)
        .catch((e) =>
          sendResponse({ ok: false, status: 0, error: String(e.message || e) })
        );
      return true;
    }
  });

  (async () => {
    const url = location.href;
    let result;
    if (isBlHost()) {
      // Same-origin via proxy — evita bloqueio Firefox a 127.0.0.1 e deadlock com o background.
      result = await lookupViaProxy(url);
      try {
        await chrome.runtime.sendMessage({
          type: "BL_LOOKUP_RESULT",
          url,
          result,
        });
      } catch {
        // ignore
      }
    } else {
      try {
        result = await chrome.runtime.sendMessage({
          type: "BL_LOOKUP_REQUEST",
          url,
        });
      } catch {
        return;
      }
    }
    if (result) render(result, url);
  })();
})();
