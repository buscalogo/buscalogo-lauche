if (typeof chrome === "undefined" && typeof browser !== "undefined") { globalThis.chrome = browser; }
const statusEl = document.getElementById("status");
const urlEl = document.getElementById("url");
const suggestBtn = document.getElementById("suggest");
const refreshBtn = document.getElementById("refresh");
const allowAgentBtn = document.getElementById("allow-agent");
const pageAlertEl = document.getElementById("page-alert");
const permHint = document.getElementById("perm-hint");

const AGENT_ORIGINS = ["http://127.0.0.1:9970/*", "http://localhost:9970/*"];
const AGENT_BASES = ["http://127.0.0.1:9970", "http://localhost:9970"];
/** Pedido no popup (gesto do usuário); no background o navegador costuma negar. */
const PAGE_ORIGINS = ["http://*/*", "https://*/*"];

let currentUrl = "";

function setStatus(text, cls) {
  statusEl.textContent = text;
  statusEl.className = "status" + (cls ? " " + cls : "");
}

function setPermHint(text) {
  if (!text) {
    permHint.style.display = "none";
    permHint.textContent = "";
    return;
  }
  permHint.style.display = "block";
  permHint.textContent = text;
}

async function getActiveTab() {
  const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
  return tabs[0];
}

/** Fallback: o popup fala direto com o Agent (útil se o background estiver bloqueado). */
async function directAgentFetch(path, options = {}) {
  const method = (options.method || "GET").toUpperCase();
  const headers = { ...(options.headers || {}) };
  if (method !== "GET" && method !== "HEAD" && !headers["Content-Type"]) {
    headers["Content-Type"] = "application/json";
  }
  let lastErr = null;
  for (const base of AGENT_BASES) {
    try {
      const r = await fetch(`${base}${path}`, {
        ...options,
        method,
        headers,
        targetAddressSpace: "loopback",
      });
      const data = await r.json().catch(() => ({}));
      return { ok: r.ok, status: r.status, data, base };
    } catch (e) {
      lastErr = e;
    }
  }
  throw lastErr || new Error("agente inacessível");
}

async function directLookup(url) {
  try {
    await chrome.permissions.request({ origins: AGENT_ORIGINS });
  } catch {
    // ignore
  }
  try {
    const ver = await directAgentFetch("/api/version");
    if (!ver.ok) throw new Error("Agent sem /api/version");
    const q = encodeURIComponent(url);
    const { ok, data, status } = await directAgentFetch(`/api/scraper/lookup?url=${q}`);
    if (!ok || !data?.success) {
      return {
        offline: false,
        indexed: false,
        error: data?.error || `Lookup HTTP ${status}`,
      };
    }
    return { offline: false, indexed: !!data.data?.indexed, ...data.data };
  } catch (e) {
    return {
      offline: true,
      indexed: false,
      error: String(e.message || e),
      need_agent_permission: true,
    };
  }
}

async function loadSettings() {
  const s = await chrome.runtime.sendMessage({ type: "BL_GET_SETTINGS" });
  pageAlertEl.checked = !!s?.pageAlertEnabled;
  if (s?.pageAlertEnabled && !s?.permission) {
    setPermHint("Ative o interruptor de novo (ou confirme a permissão) para mostrar o alerta nas páginas.");
  } else {
    setPermHint("");
  }
  return s;
}

async function refresh() {
  suggestBtn.hidden = true;
  allowAgentBtn.hidden = true;
  setStatus("Verificando…");
  const tab = await getActiveTab();
  currentUrl = tab?.url || "";
  urlEl.textContent = currentUrl || "—";

  let result = await chrome.runtime.sendMessage({
    type: "BL_LOOKUP_REQUEST",
    url: currentUrl,
  });

  // Se o background não alcança o Agent, tenta direto do popup.
  if (result?.offline) {
    const direct = await directLookup(currentUrl);
    if (!direct.offline) result = direct;
    else if (direct.error) result = { ...result, error: direct.error };
  }

  if (!result || result.skipped) {
    setStatus("Página não suportada", "");
    return;
  }
  if (result.offline) {
    setStatus(result.error || "Agente offline (127.0.0.1:9970)", "err");
    allowAgentBtn.hidden = false;
    setPermHint("O navegador pode estar bloqueando o acesso a 127.0.0.1. Use o botão abaixo ou abra o painel.");
    return;
  }
  if (result.error && !result.indexed) {
    setStatus(result.error, "warn");
    suggestBtn.hidden = false;
    return;
  }
  if (result.indexed) {
    setStatus(result.title ? `Indexada: ${result.title}` : "Página indexada", "ok");
    setPermHint("");
    return;
  }
  setStatus("Ainda não indexada", "warn");
  suggestBtn.hidden = false;
  setPermHint("");
}

pageAlertEl.addEventListener("change", async () => {
  const enabled = pageAlertEl.checked;

  // permissions.request só funciona com gesto do usuário no popup.
  if (enabled) {
    let permitted = false;
    try {
      permitted = await chrome.permissions.contains({ origins: PAGE_ORIGINS });
      if (!permitted) {
        permitted = await chrome.permissions.request({ origins: PAGE_ORIGINS });
      }
    } catch (e) {
      pageAlertEl.checked = false;
      setPermHint(String(e.message || e) || "Não foi possível pedir permissão.");
      return;
    }
    if (!permitted) {
      pageAlertEl.checked = false;
      setPermHint("Permissão negada pelo navegador.");
      return;
    }
  }

  const res = await chrome.runtime.sendMessage({
    type: "BL_SET_PAGE_ALERT",
    enabled,
  });
  if (!res?.ok && enabled) {
    pageAlertEl.checked = false;
    setPermHint(res?.message || "Não foi possível ativar o alerta nas páginas.");
    return;
  }
  if (enabled && res?.ok) {
    setPermHint("Alerta ativo nas páginas. Use × no chip para fechar sem sugerir.");
  } else if (!enabled) {
    setPermHint("Alerta na página desligado. O popup e o badge do ícone continuam.");
  } else {
    setPermHint("");
  }
});

allowAgentBtn.addEventListener("click", async () => {
  allowAgentBtn.disabled = true;
  try {
    // Pedido de permissão precisa de gesto do usuário (este clique).
    const granted = await chrome.permissions.request({ origins: AGENT_ORIGINS });
    if (!granted) {
      setStatus("Permissão ao Agent local negada pelo navegador.", "err");
      return;
    }
    const res = await chrome.runtime.sendMessage({ type: "BL_ENSURE_AGENT" });
    if (res?.ok) {
      setStatus("Conectado ao Agent local.", "ok");
      allowAgentBtn.hidden = true;
      await refresh();
      return;
    }
    // Ainda tenta direto
    await refresh();
  } catch (e) {
    setStatus(String(e.message || e), "err");
  } finally {
    allowAgentBtn.disabled = false;
  }
});

suggestBtn.addEventListener("click", async () => {
  suggestBtn.disabled = true;
  try {
    let res = await chrome.runtime.sendMessage({ type: "BL_SUGGEST", url: currentUrl });
    if (!res?.ok) {
      // fallback direto
      const r = await directAgentFetch("/api/scraper/tasks", {
        method: "POST",
        body: JSON.stringify({
          url: currentUrl,
          priority: "normal",
          type: "extension",
          discoveredFrom: "browser-extension",
        }),
      });
      if (!r.ok || !r.data?.success) throw new Error(r.data?.error || res?.error || "falha");
      res = { ok: true };
    }
    setStatus("URL enfileirada no scraper", "ok");
    suggestBtn.hidden = true;
  } catch (e) {
    setStatus(e.message || String(e), "err");
  } finally {
    suggestBtn.disabled = false;
  }
});

refreshBtn.addEventListener("click", refresh);
loadSettings().then(refresh);
