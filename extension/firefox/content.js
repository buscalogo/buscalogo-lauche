(function () {
  // Evita listeners duplicados se o background reinjetar o script ao religar o alerta.
  if (window.__buscalogoAgentChip) return;
  window.__buscalogoAgentChip = true;

  const ID = "buscalogo-agent-chip";
  const PROXY_PATH = "/__buscalogo_agent__";
  const DISMISS_KEY = "bl-chip-dismissed";

  function isBlHost() {
    return /\.bl$/i.test(location.hostname);
  }

  /** Agent só serve HTTP em *.bl; HTTPS-Only do Firefox quebra same-origin. */
  function canUsePageProxy() {
    return isBlHost() && location.protocol === "http:";
  }

  function proxyBase() {
    return "http://" + location.host + PROXY_PATH;
  }

  function dismissKey(url) {
    return DISMISS_KEY + ":" + url;
  }

  function isDismissed(url) {
    try {
      return sessionStorage.getItem(dismissKey(url)) === "1";
    } catch {
      return false;
    }
  }

  function markDismissed(url) {
    try {
      sessionStorage.setItem(dismissKey(url), "1");
    } catch {
      // ignore
    }
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

  function attachDismiss(el, url) {
    const close = document.createElement("button");
    close.type = "button";
    close.className = "bl-dismiss";
    close.setAttribute("aria-label", "Fechar alerta");
    close.title = "Fechar sem sugerir";
    close.textContent = "×";
    close.addEventListener("click", (ev) => {
      ev.preventDefault();
      ev.stopPropagation();
      markDismissed(url);
      hideChip();
    });
    el.appendChild(close);
  }

  function render(result, url) {
    if (isDismissed(url)) {
      hideChip();
      return;
    }

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
      attachDismiss(el, url);
      return;
    }
    if (result.indexed) {
      el.classList.add("bl-ok");
      el.innerHTML = "<span>Indexada no BuscaLogo</span>";
      attachDismiss(el, url);
      return;
    }
    el.classList.add("bl-suggest");
    el.innerHTML =
      '<span>Sugerir ao BuscaLogo</span><button type="button" class="bl-add">Adicionar</button>';
    attachDismiss(el, url);
    const btn = el.querySelector("button.bl-add");
    btn.addEventListener("click", async (ev) => {
      ev.preventDefault();
      ev.stopPropagation();
      btn.disabled = true;
      btn.textContent = "…";
      try {
        if (canUsePageProxy()) {
          const r = await fetch(proxyBase() + "/api/scraper/tasks", {
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
        attachDismiss(el, url);
      } catch (e) {
        btn.disabled = false;
        btn.textContent = "Adicionar";
        el.classList.add("bl-offline");
        const span = el.querySelector("span");
        if (span) span.textContent = e.message || "Erro";
      }
    });
  }

  async function proxyFetch(path, method, body) {
    if (!canUsePageProxy()) {
      throw new Error("proxy só em http://*.bl");
    }
    const opts = { method: method || "GET", headers: {} };
    if (method && method !== "GET" && method !== "HEAD") {
      opts.headers["Content-Type"] = "application/json";
      if (body) opts.body = typeof body === "string" ? body : JSON.stringify(body);
    }
    const base = proxyBase();
    const r = await fetch(base + path, opts);
    const data = await r.json().catch(() => ({}));
    return { ok: r.ok, status: r.status, data, base };
  }

  async function lookupViaProxy(pageUrl) {
    try {
      const base = proxyBase();
      const ver = await fetch(base + "/api/version");
      if (!ver.ok) throw new Error("Agent offline");
      const q = encodeURIComponent(pageUrl);
      const r = await fetch(base + "/api/scraper/lookup?url=" + q);
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
      if (!canUsePageProxy()) {
        sendResponse({
          ok: false,
          status: 0,
          error: "proxy só em http://*.bl (HTTPS-Only bloqueia)",
        });
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
    if (isDismissed(url)) return;

    let result;
    if (canUsePageProxy()) {
      result = await lookupViaProxy(url);
      if (result?.offline) {
        try {
          result = await chrome.runtime.sendMessage({
            type: "BL_LOOKUP_REQUEST",
            url,
          });
        } catch {
          // keep proxy offline result
        }
      } else {
        try {
          await chrome.runtime.sendMessage({
            type: "BL_LOOKUP_RESULT",
            url,
            result,
          });
        } catch {
          // ignore
        }
      }
    } else {
      // https://*.bl (HTTPS-Only) ou páginas normais → background + CORS na API
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
