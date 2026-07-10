const AGENT_ORIGINS = [
  "http://127.0.0.1:9970/*",
  "http://localhost:9970/*",
];
const AGENT_CANDIDATES = [
  "http://127.0.0.1:9970",
  "http://localhost:9970",
];
const SCRIPT_ID = "buscalogo-page-alert";
const PAGE_ORIGINS = ["http://*/*", "https://*/*"];

let agentBase = AGENT_CANDIDATES[0];

function isHttpUrl(url) {
  try {
    const u = new URL(url);
    return u.protocol === "http:" || u.protocol === "https:";
  } catch {
    return false;
  }
}

async function getSettings() {
  const data = await chrome.storage.local.get({ pageAlertEnabled: true });
  return { pageAlertEnabled: !!data.pageAlertEnabled };
}

async function setPageAlertEnabled(enabled) {
  await chrome.storage.local.set({ pageAlertEnabled: !!enabled });
  return syncPageAlert(!!enabled);
}

async function hasOrigins(origins) {
  try {
    return await chrome.permissions.contains({ origins });
  } catch {
    return false;
  }
}

async function requestOrigins(origins) {
  try {
    return await chrome.permissions.request({ origins });
  } catch {
    return false;
  }
}

async function ensureAgentHosts() {
  if (await hasOrigins(AGENT_ORIGINS)) return true;
  return requestOrigins(AGENT_ORIGINS);
}

async function hasPageOrigins() {
  return hasOrigins(PAGE_ORIGINS);
}

async function requestPageOrigins() {
  return requestOrigins(PAGE_ORIGINS);
}

async function registerAlertScripts() {
  try {
    await chrome.scripting.unregisterContentScripts({ ids: [SCRIPT_ID] });
  } catch {
    // ignore
  }
  const base = {
    id: SCRIPT_ID,
    matches: PAGE_ORIGINS,
    js: ["content.js"],
    css: ["content.css"],
    runAt: "document_idle",
  };
  try {
    await chrome.scripting.registerContentScripts([
      { ...base, persistAcrossSessions: true },
    ]);
  } catch {
    await chrome.scripting.registerContentScripts([base]);
  }
}

async function unregisterAlertScripts() {
  try {
    await chrome.scripting.unregisterContentScripts({ ids: [SCRIPT_ID] });
  } catch {
    // ignore
  }
}

async function syncPageAlert(enabled) {
  if (!enabled) {
    await unregisterAlertScripts();
    return { ok: true, enabled: false, permission: await hasPageOrigins() };
  }
  const permitted = await hasPageOrigins();
  if (!permitted) {
    await unregisterAlertScripts();
    return {
      ok: false,
      enabled: true,
      permission: false,
      message: "Permissão de páginas necessária para o alerta.",
    };
  }
  try {
    await registerAlertScripts();
    return { ok: true, enabled: true, permission: true };
  } catch (e) {
    return {
      ok: false,
      enabled: true,
      permission: true,
      message: String(e.message || e),
    };
  }
}

function buildFetchOptions(options = {}) {
  const method = (options.method || "GET").toUpperCase();
  const headers = { ...(options.headers || {}) };
  if (method !== "GET" && method !== "HEAD" && !headers["Content-Type"]) {
    headers["Content-Type"] = "application/json";
  }
  const opts = {
    ...options,
    method,
    headers,
  };
  // Chrome/Firefox Local Network Access (loopback)
  try {
    opts.targetAddressSpace = "loopback";
  } catch {
    // ignore
  }
  return opts;
}

async function agentFetch(path, options = {}) {
  await ensureAgentHosts();
  const fetchOpts = buildFetchOptions(options);
  const bases = [agentBase, ...AGENT_CANDIDATES.filter((b) => b !== agentBase)];
  let lastErr = null;

  for (const base of bases) {
    const ctrl = new AbortController();
    const t = setTimeout(() => ctrl.abort(), 5000);
    try {
      const r = await fetch(`${base}${path}`, {
        ...fetchOpts,
        signal: ctrl.signal,
      });
      const data = await r.json().catch(() => ({}));
      agentBase = base;
      return { ok: r.ok, status: r.status, data, base };
    } catch (e) {
      lastErr = e;
    } finally {
      clearTimeout(t);
    }
  }
  const msg = lastErr?.message || String(lastErr || "agente inacessível");
  throw new Error(msg);
}

async function pingAgent() {
  try {
    const { ok, status } = await agentFetch("/api/version");
    return ok || status === 200;
  } catch {
    try {
      const { ok } = await agentFetch("/api/status");
      return ok;
    } catch {
      return false;
    }
  }
}

async function lookupUrl(url) {
  if (!isHttpUrl(url)) {
    return { offline: false, indexed: false, skipped: true };
  }

  try {
    await ensureAgentHosts();
  } catch {
    // ignore
  }

  const online = await pingAgent();
  if (!online) {
    const hasHost = await hasOrigins(AGENT_ORIGINS);
    return {
      offline: true,
      indexed: false,
      need_agent_permission: !hasHost,
      error: hasHost
        ? "Não foi possível conectar em 127.0.0.1:9970. Confirme se o BuscaLogo Agent está rodando."
        : "Sem permissão para acessar o Agent local. Clique em “Permitir Agent local”.",
    };
  }

  try {
    const q = encodeURIComponent(url);
    const { ok, data, status } = await agentFetch(`/api/scraper/lookup?url=${q}`);
    if (!ok || !data?.success) {
      return {
        offline: false,
        indexed: false,
        error:
          data?.error ||
          `Lookup indisponível (HTTP ${status}). Atualize o BuscaLogo Agent.`,
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

async function suggestUrl(url) {
  const { ok, data } = await agentFetch("/api/scraper/tasks", {
    method: "POST",
    body: JSON.stringify({
      url,
      priority: "normal",
      type: "extension",
      discoveredFrom: "browser-extension",
    }),
  });
  if (!ok || !data?.success) {
    throw new Error(data?.error || data?.message || "falha ao enfileirar");
  }
  return data;
}

function setBadge(tabId, state) {
  const map = {
    indexed: { text: "OK", color: "#2e7d32" },
    suggest: { text: "+", color: "#e65100" },
    offline: { text: "!", color: "#c62828" },
    clear: { text: "", color: "#666" },
  };
  const m = map[state] || map.clear;
  chrome.action.setBadgeText({ tabId, text: m.text });
  chrome.action.setBadgeBackgroundColor({ tabId, color: m.color });
}

async function refreshTab(tabId, url) {
  const result = await lookupUrl(url);
  if (result.skipped) {
    setBadge(tabId, "clear");
    return result;
  }
  if (result.offline) {
    setBadge(tabId, "offline");
  } else if (result.indexed) {
    setBadge(tabId, "indexed");
  } else {
    setBadge(tabId, "suggest");
  }
  const { pageAlertEnabled } = await getSettings();
  if (pageAlertEnabled) {
    try {
      await chrome.tabs.sendMessage(tabId, { type: "BL_LOOKUP", result, url });
    } catch {
      // content script pode não estar injetado nesta aba ainda
    }
  }
  return result;
}

chrome.runtime.onInstalled.addListener(() => {
  getSettings()
    .then((s) => syncPageAlert(s.pageAlertEnabled))
    .catch(() => {});
});

chrome.runtime.onStartup.addListener(() => {
  getSettings()
    .then((s) => syncPageAlert(s.pageAlertEnabled))
    .catch(() => {});
});

chrome.permissions.onRemoved.addListener(() => {
  unregisterAlertScripts().catch(() => {});
});

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  (async () => {
    if (msg?.type === "BL_LOOKUP_REQUEST") {
      const url = msg.url || sender.tab?.url;
      const tabId = sender.tab?.id;
      if (!url) {
        sendResponse({ offline: true, error: "Sem URL" });
        return;
      }
      const result = await lookupUrl(url);
      if (tabId != null) {
        if (result.offline) setBadge(tabId, "offline");
        else if (result.indexed) setBadge(tabId, "indexed");
        else if (!result.skipped) setBadge(tabId, "suggest");
      }
      sendResponse(result);
      return;
    }
    if (msg?.type === "BL_SUGGEST") {
      try {
        const data = await suggestUrl(msg.url);
        sendResponse({ ok: true, data });
      } catch (e) {
        sendResponse({ ok: false, error: String(e.message || e) });
      }
      return;
    }
    if (msg?.type === "BL_GET_SETTINGS") {
      const settings = await getSettings();
      const permission = await hasPageOrigins();
      const agentPermission = await hasOrigins(AGENT_ORIGINS);
      const online = await pingAgent();
      sendResponse({
        ...settings,
        permission,
        agentPermission,
        online,
        agent: agentBase,
      });
      return;
    }
    if (msg?.type === "BL_ENSURE_AGENT") {
      const granted = await ensureAgentHosts();
      const online = granted ? await pingAgent() : false;
      sendResponse({
        ok: granted && online,
        granted,
        online,
        agent: agentBase,
        error: !granted
          ? "Permissão ao Agent local negada."
          : online
            ? ""
            : "Permissão ok, mas o Agent não respondeu em 127.0.0.1:9970.",
      });
      return;
    }
    if (msg?.type === "BL_SET_PAGE_ALERT") {
      const want = !!msg.enabled;
      if (want) {
        const permitted = (await hasPageOrigins()) || (await requestPageOrigins());
        if (!permitted) {
          await chrome.storage.local.set({ pageAlertEnabled: false });
          sendResponse({
            ok: false,
            enabled: false,
            permission: false,
            message: "Permissão negada pelo navegador.",
          });
          return;
        }
      }
      const sync = await setPageAlertEnabled(want);
      sendResponse(sync);
      return;
    }
    if (msg?.type === "BL_AGENT_STATUS") {
      sendResponse({
        online: await pingAgent(),
        agent: agentBase,
        agentPermission: await hasOrigins(AGENT_ORIGINS),
      });
    }
  })().catch((e) => {
    try {
      sendResponse({ ok: false, offline: true, error: String(e.message || e) });
    } catch {
      // canal fechado
    }
  });
  return true;
});

chrome.tabs.onUpdated.addListener((tabId, changeInfo, tab) => {
  if (changeInfo.status === "complete" && tab.url) {
    refreshTab(tabId, tab.url).catch(() => {});
  }
});

chrome.tabs.onActivated.addListener(async ({ tabId }) => {
  try {
    const tab = await chrome.tabs.get(tabId);
    if (tab.url) await refreshTab(tabId, tab.url);
  } catch {
    // ignore
  }
});
