if (typeof chrome === "undefined" && typeof browser !== "undefined") { globalThis.chrome = browser; }
const LOOPBACK_ORIGINS = [
  "http://127.0.0.1:9970/*",
  "http://localhost:9970/*",
];
const BL_ORIGINS = ["http://*.bl/*"];
const AGENT_ORIGINS = [...LOOPBACK_ORIGINS, ...BL_ORIGINS];
const AGENT_CANDIDATES = [
  "http://127.0.0.1:9970",
  "http://localhost:9970",
  "http://buscalogo.bl/__buscalogo_agent__",
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

function isBlHost(hostname) {
  return typeof hostname === "string" && /\.bl$/i.test(hostname);
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

async function hasAgentAccess() {
  return (
    (await hasOrigins(LOOPBACK_ORIGINS)) || (await hasOrigins(BL_ORIGINS))
  );
}

async function ensureAgentHosts() {
  if (await hasAgentAccess()) return true;
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
  const opts = { method, headers };
  if (options.body != null) opts.body = options.body;
  try {
    opts.targetAddressSpace = "loopback";
  } catch {
    // ignore
  }
  return opts;
}

async function rawFetch(base, path, options = {}) {
  // Nunca usar https://*.bl — o Agent só escuta HTTP :80.
  if (/^https:\/\/[^/]+\.bl\b/i.test(base)) {
    base = base.replace(/^https:/i, "http:");
  }
  const fetchOpts = buildFetchOptions(options);
  // Proxy em *.bl não é loopback “direto” do ponto de vista do documento .bl
  if (base.includes("/__buscalogo_agent__") || /\.bl(?::\d+)?$/i.test(new URL(base, "http://x").hostname || "")) {
    delete fetchOpts.targetAddressSpace;
  }
  const ctrl = new AbortController();
  const t = setTimeout(() => ctrl.abort(), 5000);
  try {
    const r = await fetch(`${base}${path}`, { ...fetchOpts, signal: ctrl.signal });
    const data = await r.json().catch(() => ({}));
    return { ok: r.ok, status: r.status, data, base };
  } finally {
    clearTimeout(t);
  }
}

/** Pede ao content script da aba ativa para falar com o Agent via same-origin (.bl). */
async function fetchViaActiveBlTab(path, options = {}) {
  const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
  const tab = tabs[0];
  if (!tab?.id || !tab.url) return null;
  let host = "";
  try {
    host = new URL(tab.url).hostname;
  } catch {
    return null;
  }
  if (!isBlHost(host)) return null;
  try {
    return await chrome.tabs.sendMessage(tab.id, {
      type: "BL_AGENT_FETCH",
      path,
      method: options.method || "GET",
      body: options.body || null,
    });
  } catch {
    return null;
  }
}

async function agentFetch(path, options = {}) {
  await ensureAgentHosts().catch(() => false);

  // Via content script em aba *.bl (Firefox bloqueia fetch direto a 127.0.0.1).
  // Não usar quando o pedido veio do próprio content (evita deadlock).
  if (!options.skipViaTab) {
    const viaTab = await fetchViaActiveBlTab(path, options);
    if (viaTab && !viaTab.error && (viaTab.ok || viaTab.status > 0)) {
      if (viaTab.base && !/^https:/i.test(viaTab.base)) agentBase = viaTab.base;
      return viaTab;
    }
  }

  const bases = [];
  for (const b of [agentBase, ...AGENT_CANDIDATES]) {
    if (b && !bases.includes(b)) bases.push(b);
  }

  let lastErr = null;
  for (const base of bases) {
    try {
      const res = await rawFetch(base, path, options);
      agentBase = base;
      return res;
    } catch (e) {
      lastErr = e;
    }
  }
  throw lastErr || new Error("agente inacessível");
}

async function pingAgent() {
  return pingAgentWithOpts();
}

async function lookupUrl(url, fetchOpts = {}) {
  if (!isHttpUrl(url)) {
    return { offline: false, indexed: false, skipped: true };
  }

  const online = await pingAgentWithOpts(fetchOpts);
  if (!online) {
    const hasHost = await hasAgentAccess();
    return {
      offline: true,
      indexed: false,
      need_agent_permission: !hasHost,
      error: hasHost
        ? "Não foi possível conectar ao Agent. No Firefox, use um site .bl (proxy local) ou permita acesso a 127.0.0.1."
        : "Sem permissão para acessar o Agent local. Clique em “Permitir Agent local”.",
    };
  }

  try {
    const q = encodeURIComponent(url);
    const { ok, data, status } = await agentFetch(
      `/api/scraper/lookup?url=${q}`,
      fetchOpts
    );
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
    return { offline: true, indexed: false, error: String(e.message || e) };
  }
}

async function pingAgentWithOpts(fetchOpts = {}) {
  try {
    const { ok, status } = await agentFetch("/api/version", fetchOpts);
    return ok || status === 200;
  } catch {
    try {
      const { ok } = await agentFetch("/api/status", fetchOpts);
      return ok;
    } catch {
      return false;
    }
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
      // ignore
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
    if (msg?.type === "BL_LOOKUP_RESULT") {
      const tabId = sender.tab?.id;
      const result = msg.result || {};
      if (tabId != null) {
        if (result.offline) setBadge(tabId, "offline");
        else if (result.indexed) setBadge(tabId, "indexed");
        else if (!result.skipped) setBadge(tabId, "suggest");
      }
      sendResponse({ ok: true });
      return;
    }
    if (msg?.type === "BL_LOOKUP_REQUEST") {
      const url = msg.url || sender.tab?.url;
      const tabId = sender.tab?.id;
      if (!url) {
        sendResponse({ offline: true, error: "Sem URL" });
        return;
      }
      // Pedido do content script: não reenviar fetch para a mesma aba.
      const result = await lookupUrl(url, {
        skipViaTab: sender.tab?.id != null,
      });
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
      sendResponse({
        ...settings,
        permission: await hasPageOrigins(),
        agentPermission: await hasAgentAccess(),
        online: await pingAgent(),
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
            : "Permissão ok, mas o Agent não respondeu. Abra http://buscalogo.bl e tente de novo.",
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
      sendResponse(await setPageAlertEnabled(want));
      return;
    }
    if (msg?.type === "BL_AGENT_STATUS") {
      sendResponse({
        online: await pingAgent(),
        agent: agentBase,
        agentPermission: await hasAgentAccess(),
      });
    }
  })().catch((e) => {
    try {
      sendResponse({ ok: false, offline: true, error: String(e.message || e) });
    } catch {
      // ignore
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
