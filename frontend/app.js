const $ = (s) => document.querySelector(s);
const $$ = (s) => document.querySelectorAll(s);
const fmtUptime = (s) => {
  if (!s || s < 1) return "\u2014";
  const h = Math.floor(s/3600), m = Math.floor((s%3600)/60), sec = s%60;
  return h > 0 ? `${h}h ${m}m` : m > 0 ? `${m}m ${sec}s` : `${sec}s`;
};
const stateLabel = (st) => ({running:"rodando",stopped:"parado",starting:"iniciando",stopping:"parando",crashed:"caiu",disabled:"desabilitado"}[st] || st);

const logLines = [];
let logFilter = "";
let currentConfig = null;
let currentWebPort = 80;
let currentDNSMode = "local";
let yggAddr = "";
let configDirty = false;
let p2pConfigDirty = false;
let updateBusy = false;
let updateDismissed = false;
let lastUpdateStatus = null;

/* Tab switching */
document.querySelectorAll(".tab").forEach(tab => {
  tab.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach(t => t.classList.remove("active"));
    document.querySelectorAll(".tab-panel").forEach(p => p.classList.remove("active"));
    tab.classList.add("active");
    const panel = document.getElementById("tab-" + tab.dataset.tab);
    if (panel) panel.classList.add("active");
    if (tab.dataset.tab === "scraper") refreshScraperInfo();
  });
});

/* Toast */
function toast(msg, dur) {
  const el = $("#toast");
  el.textContent = msg;
  el.style.display = "block";
  clearTimeout(el._timer);
  el._timer = setTimeout(() => { el.style.display = "none"; }, dur || 2000);
}

/* Modal helpers */
function openModal(id) { $(`#${id}`).style.display = "flex"; }
function closeModal(id) { $(`#${id}`).style.display = "none"; }

document.querySelectorAll("[data-modal]").forEach(btn => {
  btn.addEventListener("click", () => closeModal(btn.dataset.modal));
});
document.querySelectorAll(".modal").forEach(m => {
  m.addEventListener("click", (e) => { if (e.target === m) m.style.display = "none"; });
});

/* Copy to clipboard */
async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast("Copiado!");
  } catch {
    toast("Erro ao copiar");
  }
}

function updateCard(id, st) {
  const card = $("#card-" + id);
  if (!card) return;
  const dot = card.querySelector(".dot");
  if (dot) dot.dataset.state = st.state;
  const stateEl = card.querySelector(".state");
  if (stateEl) stateEl.textContent = stateLabel(st.state);
  const pidEl = card.querySelector(".pid");
  if (pidEl) pidEl.textContent = st.pid || "\u2014";
  const uptimeEl = card.querySelector(".uptime");
  if (uptimeEl) uptimeEl.textContent = fmtUptime(st.uptime_seconds);
  const restartsEl = card.querySelector(".restarts");
  if (restartsEl) restartsEl.textContent = st.restart_count;
}

async function fetchStatus() {
  try {
    const r = await fetch("/api/status");
    const d = await r.json();
    currentDNSMode = d.dns_mode || "local";
    $("#node-name").textContent = d.node.name || "BuscaLogo Node";
    $("#dns-badge").textContent = "DNS: " + (currentDNSMode === "system" ? "sistema :53" : "local :5333");
    updateCard("coredns", d.services.coredns);
    updateCard("yggdrasil", d.services.yggdrasil);
    updateCard("couchdb", d.services.couchdb);
    if (d.services.scraper) updateCard("scraper", d.services.scraper);
    refreshYggdrasilInfo();
    refreshCouchInfo();
    const scraperTab = document.getElementById("tab-scraper");
    if (scraperTab?.classList.contains("active")) refreshScraperInfo();

    const sys = d.system;
    $("#sys-mode-badge").textContent = d.dns_mode === "system" ? "Modo B" : "Modo A";
    $("#dns-mode-badge").textContent = d.dns_mode === "system" ? "Sistema (:53)" : "Local (:5333)";

    currentConfig = d.config || {};
    if (!configDirty) fillConfig(d.config);
    refreshLogSources();
    renderSites();
    renderSystrayWarning(d.systray);
    if (d.version) $("#version").textContent = `BuscaLogo Agent v${d.version}`;
    refreshUpdateStatus();
    if (d.web) {
      const w = d.web;
      currentWebPort = w.running ? (w.actual_port || w.port || 80) : (w.actual_port || w.port || 80);
      const wBadge = $("#web-port-badge");
      const wBadge2 = $("#web-port-badge-2");
      const whint = $("#web-hint");
      const actual = w.actual_port || w.port || 80;
      [wBadge, wBadge2].forEach(el => { if (el) el.textContent = w.running ? `:${actual}` : "off"; });
      if (!w.running) {
        [wBadge, wBadge2].forEach(el => { if (el) { el.className = "badge warn"; } });
        whint.innerHTML = `<b>Servidor web parado.</b> ${escapeHtml(w.error || "Porta 80 indisponível.")} Use <b>Reiniciar web</b> ou libere a porta 80 (Apache/Nginx).`;
        whint.style.color = "var(--red)";
      } else if (actual === 80 && !w.fallback) {
        [wBadge, wBadge2].forEach(el => { if (el) { el.className = "badge ok"; } });
        whint.textContent = "Servindo .bl na porta 80 (acesse http://<host>.bl).";
        whint.style.color = "";
      } else if (w.fallback) {
        [wBadge, wBadge2].forEach(el => { if (el) { el.className = "badge warn"; } });
        whint.innerHTML = `Porta 80 indisponível. Rodando na porta <b>${actual}</b>. Use <code>http://&lt;host&gt;.bl:${actual}</code> no navegador.`;
        whint.style.color = "var(--amber)";
      } else {
        [wBadge, wBadge2].forEach(el => { if (el) { el.className = "badge"; } });
        whint.textContent = `Servindo .bl na porta ${actual}.`;
        whint.style.color = "";
      }
      const btn = $("#web-enable-80");
      const restartBtn = $("#web-restart");
      if (w.running && actual === 80 && !w.fallback) {
        btn.textContent = "Porta 80 ativa";
        btn.disabled = true;
        btn.classList.add("ok");
      } else {
        btn.textContent = "Ativar porta 80";
        btn.disabled = false;
        btn.classList.remove("ok");
      }
      if (restartBtn) restartBtn.disabled = false;
    }
    $("#api-badge").textContent = "online";
    $("#api-badge").className = "badge ok";
    $("#api-badge-2").textContent = "online";
    $("#api-badge-2").className = "badge ok";

    const autoStartEnabled = d.autostart === true;
    $("#autostart-enable").style.display = autoStartEnabled ? "none" : "inline-block";
    $("#autostart-disable").style.display = autoStartEnabled ? "inline-block" : "none";
    $("#autostart-status").textContent = autoStartEnabled ? "Status: ativo" : "Status: inativo";
  } catch (e) {
    $("#api-badge").textContent = "offline";
    $("#api-badge").className = "badge warn";
    $("#api-badge-2").textContent = "offline";
    $("#api-badge-2").className = "badge warn";
  }
}

async function fetchDNSStatus() {
  try {
    const r = await fetch("/api/dns/status");
    const d = await r.json();

    $("#dns-status-mode").textContent = d.dns_mode === "system" ? "Sistema (:53)" : "Local (:5333)";
    $("#dns-status-listen").textContent = `${d.listen || "127.0.0.1"}:${d.port || 5333}`;
    const cs = d.coredns_status || {};
    $("#dns-status-state").textContent = stateLabel(cs.state || "stopped");
    $("#dns-status-pid").textContent = cs.pid || "\u2014";
    $("#dns-status-uptime").textContent = fmtUptime(cs.uptime_seconds);
    $("#dns-status-restarts").textContent = cs.restart_count ?? "\u2014";

    // Upstreams
    const upEl = $("#dns-upstreams");
    const upList = d.upstream || [];
    upEl.innerHTML = upList.length
      ? upList.map(u => upstreamItem(u)).join("")
      : '<div class="muted">Nenhum upstream configurado.</div>';

    // Yggdns
    const yggBlock = $("#dns-yggdns");
    const yggList = $("#dns-yggdns-list");
    if (d.yggdns_enabled && Array.isArray(d.yggdns) && d.yggdns.length > 0) {
      yggBlock.style.display = "block";
      yggList.innerHTML = d.yggdns.map(u => `<div style="padding:2px 0">\u2022 ${escapeHtml(u)}</div>`).join("");
    } else {
      yggBlock.style.display = "none";
    }

    // TLDs
    $("#dns-tlds").textContent = (d.search_domains || []).join(", ") || "\u2014";

    // System info
    const sys = d.system || {};
    $("#dns-sys-port53").innerHTML = sys.port_53_loopback_free
      ? '<span style="color:var(--green)">livre \u2713</span>'
      : '<span style="color:var(--red)">ocupado</span>';
    $("#dns-sys-resolved").textContent = sys.has_systemd_resolved ? (sys.resolved_stub_active ? "stub ativo" : "ativo (foreign)") : "n\u00e3o";
    $("#dns-sys-resolv").textContent = sys.resolv_conf_mode || "\u2014";
    $("#dns-sys-nm").textContent = sys.has_network_manager ? "sim" : "n\u00e3o";
    $("#dns-sys-setcap").innerHTML = sys.setcap_available ? '<span style="color:var(--green)">dispon\u00edvel</span>' : '<span style="color:var(--red)">indispon\u00edvel</span>';
    $("#dns-sys-uses").innerHTML = sys.uses_buscalogo_dns ? '<span style="color:var(--green)">sim</span>' : "n\u00e3o";

    // Corefile
    $("#dns-corefile").textContent = d.corefile || "# Corefile n\u00e3o encontrado";

    // Hint
    const hint = $("#dns-hint");
    if (d.dns_mode === "local") {
      hint.textContent = "Modo A: coredns em 127.0.0.1:5333 (sem root). Ative o Modo B para resolver .bl em todo o sistema via :53.";
    } else {
      hint.textContent = "Modo B ativo: coredns em 127.0.0.1:53 + resolvedor integrado. Revers\u00edvel a qualquer momento.";
    }
    $("#dns-enable").disabled = d.dns_mode === "system";
    $("#dns-disable").disabled = d.dns_mode !== "system";

    // External listen / Yggdrasil DNS
    const extBtn = $("#dns-external");
    const ext = d.external_listen === true;
    if (ext) {
      extBtn.textContent = "Fechar para rede externa";
      extBtn.className = "btn red";
    } else {
      extBtn.textContent = "Abrir para rede Yggdrasil";
      extBtn.className = "btn";
    }

    const yggRow = $("#dns-ygg-addr-row");
    const yggAddr = $("#dns-ygg-addr");
    if (ext && d.ygg_ip) {
      const dnsAddr = `[${d.ygg_ip}]:${d.port || 5333}`;
      yggAddr.textContent = dnsAddr;
      yggRow.style.display = "flex";
    } else {
      yggRow.style.display = "none";
    }
  } catch (e) {
    // DNS tab not critical, silently handle
  }
}

async function doAutostart(enable) {
  const btn = enable ? $("#autostart-enable") : $("#autostart-disable");
  const orig = btn.textContent;
  btn.disabled = true;
  btn.textContent = "aplicando...";
  try {
    const r = await fetch(`/api/autostart/${enable ? "enable" : "disable"}`, { method: "POST" });
    btn.textContent = r.ok ? "feito \u2713" : "erro \u2717";
    btn.style.color = r.ok ? "var(--green)" : "var(--red)";
  } catch {
    btn.textContent = "erro \u2717";
    btn.style.color = "var(--red)";
  }
  setTimeout(() => {
    btn.textContent = orig;
    btn.style.color = "";
    btn.disabled = false;
    fetchStatus();
  }, 2000);
}

function markConfigDirty() {
  configDirty = true;
}

function fillConfig(c) {
  if (!c) return;
  $("#cfg-node-name").value = c.node?.name || "";
  $("#cfg-ygg-mode").value = c.yggdrasil?.mode || "own";
  $("#cfg-ygg-ext").value = c.yggdrasil?.external_binary || "";
  $("#cfg-ygg-peers").value = (c.yggdrasil?.peers || []).join("\n");
  $("#cfg-dns-upstream").value = (c.dns?.upstream || []).join(", ");
  $("#cfg-dns-tlds").value = (c.dns?.search_domains || []).join(", ");
  $("#cfg-scraper-enabled").checked = c.scraper?.enabled === true;
  $("#cfg-scraper-concurrent").value = c.scraper?.max_concurrent || 3;
  $("#cfg-scraper-depth").value = c.scraper?.max_depth || 3;
  $("#cfg-scraper-schedule").value = c.scraper?.default_schedule_days ?? 7;
}

function markP2PConfigDirty() {
  p2pConfigDirty = true;
}

function applyP2PConfigToUI(d) {
  const p = d.p2p || {};
  const pathEl = $("#p2p-config-path");
  const yamlEl = $("#p2p-config-yaml");
  const enabledEl = $("#p2p-simple-enabled");
  const urlsEl = $("#p2p-simple-urls");
  const maxEl = $("#p2p-simple-max-results");
  if (pathEl) {
    pathEl.textContent = d.path ? `${d.path} (seção p2p)` : "config.yaml (seção p2p)";
  }
  if (yamlEl && d.yaml != null) yamlEl.value = d.yaml;
  if (enabledEl) enabledEl.checked = p.enabled === true;
  if (urlsEl) urlsEl.value = (p.signaling_urls || []).join("\n");
  if (maxEl) maxEl.value = p.max_results_per_query > 0 ? p.max_results_per_query : 50;
}

async function loadP2PConfig(force) {
  if (p2pConfigDirty && !force) return;
  try {
    const r = await fetch("/api/p2p/config");
    const d = await r.json();
    if (!r.ok || !d.ok) return;
    applyP2PConfigToUI(d);
    if (force) p2pConfigDirty = false;
  } catch {
    // ignore
  }
}

function readP2PSimpleForm() {
  const urls = ($("#p2p-simple-urls")?.value || "")
    .split("\n").map(s => s.trim()).filter(Boolean);
  const max = parseInt($("#p2p-simple-max-results")?.value, 10) || 50;
  return {
    enabled: $("#p2p-simple-enabled")?.checked === true,
    signaling_urls: urls,
    max_results_per_query: Math.min(200, Math.max(1, max)),
  };
}

async function saveP2PConfigResponse(r, btn, origLabel) {
  const d = await r.json();
  if (!r.ok || !d.ok) throw new Error(d.error || "falha ao salvar");
  p2pConfigDirty = false;
  applyP2PConfigToUI(d);
  toast(d.message || "Configuração signaling salva");
  setTimeout(refreshScraperInfo, 600);
  if (btn) { btn.disabled = false; btn.textContent = origLabel; }
}

async function saveP2PConfigSimple() {
  const btn = $("#p2p-cfg-save-simple");
  const orig = btn?.textContent || "Salvar";
  if (btn) { btn.disabled = true; btn.textContent = "salvando…"; }
  try {
    const p2p = readP2PSimpleForm();
    if (p2p.enabled && p2p.signaling_urls.length === 0) {
      throw new Error("informe ao menos um servidor de signaling");
    }
    const r = await fetch("/api/p2p/config", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ p2p }),
    });
    await saveP2PConfigResponse(r, btn, orig);
  } catch (e) {
    toast("Erro: " + (e.message || e), 4000);
    if (btn) { btn.disabled = false; btn.textContent = orig; }
  }
}

async function saveP2PConfigAdvanced() {
  const yamlEl = $("#p2p-config-yaml");
  const btn = $("#p2p-cfg-save-advanced");
  if (!yamlEl) return;
  if (!confirm("Salvar o YAML manualmente? Um erro de sintaxe pode impedir o P2P de funcionar.")) return;
  const orig = btn?.textContent || "Salvar YAML";
  if (btn) { btn.disabled = true; btn.textContent = "salvando…"; }
  try {
    const r = await fetch("/api/p2p/config", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ yaml: yamlEl.value }),
    });
    await saveP2PConfigResponse(r, btn, orig);
  } catch (e) {
    toast("Erro: " + (e.message || e), 4000);
    if (btn) { btn.disabled = false; btn.textContent = orig; }
  }
}

async function resetP2PConfig() {
  if (!confirm("Restaurar a configuração P2P ao padrão?\n\n• Conectar: ativado\n• Signaling: wss://api.buscalogo.com\n• Máx. resultados: 50")) return;
  const btn = $("#p2p-cfg-reset");
  const orig = btn?.textContent || "Restaurar padrão";
  if (btn) { btn.disabled = true; btn.textContent = "restaurando…"; }
  try {
    const r = await fetch("/api/p2p/config/reset", { method: "POST" });
    await saveP2PConfigResponse(r, btn, orig);
  } catch (e) {
    toast("Erro: " + (e.message || e), 4000);
    if (btn) { btn.disabled = false; btn.textContent = orig; }
  }
}

function renderSystrayWarning(info) {
  const banner = $("#systray-warning");
  if (!banner) return;
  if (!info || info.ok) {
    banner.style.display = "none";
    return;
  }
  banner.style.display = "flex";
  $("#systray-msg").textContent = info.warning || "ambiente sem suporte";
  $("#systray-help").textContent = info.details || "";
}

async function regenerateCouchPassword() {
  if (!confirm("Gerar nova senha do CouchDB? O banco ser\u00e1 reiniciado e conex\u00f5es ativas precisar\u00e3o da nova credencial.")) return;
  const btn = $("#couch-regen-pass");
  if (btn) btn.disabled = true;
  try {
    const r = await fetch("/api/couchdb/regenerate-password", { method: "POST" });
    const d = await r.json();
    if (!r.ok || !d.ok) throw new Error(d.error || "falha ao regenerar senha");
    const passEl = $("#couch-admin-pass");
    const userEl = $("#couch-admin-user");
    if (userEl) userEl.textContent = d.admin_user || "\u2014";
    if (passEl) {
      passEl.textContent = d.admin_password || "\u2014";
      passEl.dataset.secret = d.admin_password || "";
    }
    toast("Nova senha gerada!");
    fetchStatus();
    refreshCouchInfo();
  } catch (e) {
    toast("Erro: " + (e.message || e), 4000);
  } finally {
    if (btn) btn.disabled = false;
  }
}

async function refreshScraperInfo() {
  try {
    const [infoR, statsR, tasksR, resultsR, p2pR] = await Promise.all([
      fetch("/api/scraper/info"),
      fetch("/api/scraper/stats"),
      fetch("/api/scraper/tasks"),
      fetch("/api/scraper/results?limit=10"),
      fetch("/api/p2p/status"),
    ]);
    loadP2PConfig(false);
    const info = (await infoR.json()).info || {};
    const stats = (await statsR.json()).data || {};
    const tasks = (await tasksR.json()).data || {};
    const results = (await resultsR.json()).data || [];

    const queueEl = $("#scraper-queue");
    const processedEl = $("#scraper-processed");
    const dbEl = $("#scraper-couch-mode");
    if (dbEl) dbEl.textContent = info.database ? `CouchDB / ${info.database}` : "CouchDB";
    const q = stats.queues || {};
    const queued = (q.high || 0) + (q.normal || 0) + (q.low || 0) + (q.discovered || 0);
    if (queueEl) queueEl.textContent = info.running ? `${queued} na fila, ${stats.active || 0} ativa(s)` : "parado";
    if (processedEl) processedEl.textContent = stats.metrics?.total_processed ?? "0";

    const activeEl = $("#scraper-active-count");
    const queuedEl = $("#scraper-queued-count");
    if (activeEl) activeEl.textContent = stats.active ?? (tasks.active || []).length;
    if (queuedEl) queuedEl.textContent = queued;
    const successEl = $("#scraper-success-count");
    const failedEl = $("#scraper-failed-count");
    const linksEl = $("#scraper-links-count");
    const avgEl = $("#scraper-avg-time");
    const m = stats.metrics || {};
    if (successEl) successEl.textContent = m.successful ?? 0;
    if (failedEl) failedEl.textContent = m.failed ?? 0;
    if (linksEl) linksEl.textContent = m.links_discovered ?? 0;
    if (avgEl) {
      const ms = m.avg_processing_time_ms;
      avgEl.textContent = ms > 0 ? `${Math.round(ms)} ms` : "—";
    }

    const listEl = $("#scraper-results");
    if (listEl) {
      if (!results.length) {
        listEl.innerHTML = '<span class="muted">Nenhum resultado ainda.</span>';
      } else {
        listEl.innerHTML = results.map(r => {
          const title = escapeHtml(r.title || r.url || r._id || "?");
          const host = escapeHtml(r.hostname || "");
          return `<div class="scraper-result-row"><a href="${escapeHtml(r.url || "#")}" target="_blank" rel="noopener">${title}</a><span class="muted">${host}</span></div>`;
        }).join("");
      }
    }

    try {
      const p2p = await p2pR.json();
      const st = p2p.stats || {};
      const dot = $("#p2p-dot");
      const connEl = $("#p2p-connected");
      const detailEl = $("#p2p-status-detail");
      const enabled = st.enabled === true || p2p.config?.enabled === true;
      const running = st.running === true;
      if (dot) {
        if (p2p.connected) dot.dataset.state = "running";
        else if (!enabled) dot.dataset.state = "stopped";
        else if (running) dot.dataset.state = "starting";
        else dot.dataset.state = "stopped";
      }
      if (connEl) {
        connEl.textContent = p2p.connected
          ? `${st.connected_count || 1}/${st.total_signalings || "?"} conectado(s)`
          : (st.message || p2p.message || "desconectado");
        connEl.style.color = p2p.connected ? "var(--green)" : (!enabled ? "var(--muted)" : "var(--amber)");
      }
      if ($("#p2p-peer-id")) $("#p2p-peer-id").textContent = st.peer_id || "—";
      if ($("#p2p-queries")) $("#p2p-queries").textContent = st.queries_responded ?? 0;
      if ($("#p2p-results-sent")) $("#p2p-results-sent").textContent = st.total_results_sent ?? 0;
      if (detailEl) {
        const lines = [];
        if (st.start_error) lines.push(st.start_error);
        for (const sig of st.signalings || []) {
          if (sig.state === "connected") {
            lines.push(`${sig.url}: conectado`);
          } else if (sig.last_error) {
            lines.push(`${sig.url}: ${sig.last_error}`);
          } else if (sig.state === "gave_up") {
            lines.push(`${sig.url}: desistiu após ${sig.attempts || "?"} tentativas`);
          } else if (sig.state === "connecting" || sig.state === "reconnecting") {
            lines.push(`${sig.url}: reconectando…`);
          } else if (sig.state === "error") {
            lines.push(`${sig.url}: falha na conexão`);
          }
        }
        if (!enabled && !lines.length) {
          lines.push("P2P desabilitado — ative em Configuração signaling abaixo e salve.");
        }
        if (enabled && !running && !lines.length) {
          lines.push("Serviço P2P parado — clique em Reconectar P2P ou salve a configuração.");
        }
        if (lines.length) {
          detailEl.style.display = "block";
          detailEl.innerHTML = lines.map(l => `<div>${escapeHtml(l)}</div>`).join("");
        } else {
          detailEl.style.display = "none";
          detailEl.innerHTML = "";
        }
      }
    } catch {
      // ignore p2p status errors
    }
  } catch {
    const queueEl = $("#scraper-queue");
    if (queueEl) queueEl.textContent = "erro";
  }
}

async function addScraperURL() {
  const url = $("#scraper-url-input")?.value?.trim();
  if (!url) { toast("Informe uma URL"); return; }
  const priority = $("#scraper-priority")?.value || "normal";
  const btn = $("#scraper-add-url");
  if (btn) btn.disabled = true;
  try {
    const r = await fetch("/api/scraper/tasks", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ url, priority }),
    });
    const d = await r.json();
    if (!r.ok || !d.success) throw new Error(d.error || d.message || "falha");
    toast("URL enfileirada!");
    $("#scraper-url-input").value = "";
    refreshScraperInfo();
    fetchStatus();
  } catch (e) {
    toast("Erro: " + (e.message || e), 4000);
  } finally {
    if (btn) btn.disabled = false;
  }
}

async function clearScraperQueue() {
  if (!confirm("Limpar todas as tarefas na fila?")) return;
  try {
    const r = await fetch("/api/scraper/clear", { method: "POST" });
    const d = await r.json();
    if (!r.ok || !d.success) throw new Error(d.error || "falha");
    toast("Fila limpa");
    refreshScraperInfo();
  } catch (e) {
    toast("Erro: " + (e.message || e), 4000);
  }
}

async function refreshCouchInfo() {
  try {
    const r = await fetch("/api/couchdb/info");
    const d = await r.json();
    const info = d.info || {};
    const urlEl = $("#couch-url");
    const countEl = $("#couch-dbs-count");
    const userEl = $("#couch-admin-user");
    const passEl = $("#couch-admin-pass");
    if (urlEl) urlEl.textContent = info.url || "\u2014";
    if (userEl) userEl.textContent = info.admin_user || "\u2014";
    if (passEl) {
      passEl.textContent = info.admin_password || "(reinicie o agente para gerar)";
      passEl.dataset.secret = info.admin_password || "";
    }
    if (countEl) {
      const dbs = info.databases || [];
      countEl.textContent = info.reachable ? `${dbs.length} (${info.version || "?"})` : "offline";
    }
  } catch {
    const countEl = $("#couch-dbs-count");
    if (countEl) countEl.textContent = "erro";
  }
}

async function refreshYggdrasilInfo() {
  try {
    const r = await fetch("/api/yggdrasil/info");
    const d = await r.json();
    const info = d.info || {};
    if (info.reachable && info.self) {
      yggAddr = info.self.address || "";
      $("#ygg-addr").textContent = yggAddr;
      $("#ygg-addr").title = info.self.key || "";
      $("#ygg-addr-display").textContent = yggAddr;
      $("#ygg-pubkey").textContent = info.self.key || "";
      $("#ygg-dot-network").dataset.state = "running";
      renderPeers(info.peers || []);
    } else {
      const placeholder = info.socket_exists ? "socket OK, aguardando..." : "n\u00e3o conectado";
      $("#ygg-addr").textContent = placeholder;
      $("#ygg-addr").title = info.error || "";
      $("#ygg-addr-display").textContent = placeholder;
      $("#ygg-pubkey").textContent = "\u2014";
      $("#ygg-dot-network").dataset.state = "stopped";
      renderPeers([]);
    }
    $("#ygg-peers-count").textContent = Array.isArray(info.peers) ? info.peers.length : "\u2014";
  } catch (e) {
    $("#ygg-addr").textContent = "\u2014";
    $("#ygg-addr-display").textContent = "\u2014";
    $("#ygg-peers-count").textContent = "\u2014";
    $("#ygg-pubkey").textContent = "\u2014";
    $("#ygg-dot-network").dataset.state = "stopped";
    renderPeers([]);
  }
}

function renderPeers(peers) {
  const container = $("#ygg-peers-list");
  const badge = $("#ygg-peers-badge");
  if (!container) return;
  const count = Array.isArray(peers) ? peers.length : 0;
  if (badge) badge.textContent = count;
  container.innerHTML = "";
  if (count === 0) {
    container.innerHTML = '<div class="muted" style="padding:8px 0">Nenhum peer conectado.</div>';
    return;
  }
  peers.forEach(p => {
    const row = document.createElement("div");
    row.className = "peer-row";
    const uptime = p.uptime ? fmtUptime(p.uptime) : "";
    const ep = p.endpoint || "";
    const key = p.key ? p.key.substring(0, 16) + "..." : "";
    row.innerHTML = `
      <span class="peer-endpoint" title="${escapeAttr(ep)}">${escapeHtml(ep)}</span>
      <span class="peer-key" title="${escapeAttr(p.key || "")}">${escapeHtml(key)}</span>
      <span class="peer-uptime">${uptime}</span>
    `;
    container.appendChild(row);
  });
}

async function openExternal(url) {
  try {
    const r = await fetch("/api/open-url", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ url }),
    });
    if (!r.ok) {
      const e = await r.json().catch(() => ({}));
      toast("Erro ao abrir: " + (e.error || "falha"), 3000);
      return false;
    }
    return true;
  } catch {
    window.open(url, "_blank", "noopener,noreferrer");
    return true;
  }
}

async function renderSites() {
  try {
    const r = await fetch("/api/sites");
    const d = await r.json();
    const sites = d.sites || [];
    $("#sites-list").innerHTML = "";
    if (sites.length === 0) {
      $("#sites-list").innerHTML = '<div class="muted" style="padding:8px 0">Nenhum site configurado.</div>';
      return;
    }
    sites.forEach(s => {
      const row = document.createElement("div");
      row.className = "site-row" + (s.enabled ? " site-row-open" : "");
      const detail = s.type === "proxy" ? (s.upstream || "proxy") : (s.root || "static");
      const linkUrl = siteUrl(s.host, currentWebPort, currentDNSMode);
      row.innerHTML = `
        <button type="button" class="site-host${s.enabled ? "" : " disabled"}" data-site-open="${escapeAttr(linkUrl)}" title="${escapeAttr(linkUrl)}" ${s.enabled ? "" : "disabled"}>${escapeHtml(s.host)}</button>
        <span class="site-type">${escapeHtml(s.type || "static")}</span>
        <span class="site-detail">${escapeHtml(detail)}</span>
        <span class="site-status">${s.enabled ? "ativo" : "inativo"}</span>
        <button type="button" class="btn small site-open-btn" data-site-open="${escapeAttr(linkUrl)}" ${s.enabled ? "" : "disabled"}>abrir</button>
        <button type="button" class="btn small red" data-site-del="${escapeAttr(s.host)}">remover</button>
      `;
      $("#sites-list").appendChild(row);
    });
    $("#sites-list").querySelectorAll("[data-site-open]").forEach(btn => {
      btn.addEventListener("click", async (e) => {
        e.stopPropagation();
        if (btn.disabled) return;
        await openExternal(btn.dataset.siteOpen);
      });
    });
    $("#sites-list").querySelectorAll("button[data-site-del]").forEach(btn => {
      btn.addEventListener("click", () => deleteSite(btn.dataset.siteDel));
    });
  } catch (e) {
    $("#sites-list").innerHTML = '<div class="muted" style="padding:8px 0;color:var(--red)">Erro ao carregar sites.</div>';
  }
}

function escapeAttr(s) { return (s || "").replace(/"/g, "&quot;"); }
function escapeHtml(s) {
  return (s || "").replace(/[&<>"']/g, c => ({ "&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;" }[c]));
}

function upstreamItem(s) {
  return `<div style="display:flex;align-items:center;gap:6px;padding:2px 0">
    <span style="flex:1">\u2022 ${escapeHtml(s)}</span>
    <button class="btn mini red" onclick="removeUpstream('${escapeHtml(s)}')" title="Remover">\u2715</button>
  </div>`;
}

async function addUpstream() {
  const input = $("#dns-upstream-input");
  const server = input.value.trim();
  if (!server) return;
  input.disabled = true;
  try {
    const r = await fetch("/api/dns/upstream", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({server}),
    });
    if (r.ok) {
      input.value = "";
      fetchDNSStatus();
    } else {
      const e = await r.json();
      toast("Erro: " + (e.error || "falha"), 3000);
    }
  } catch {
    toast("Erro de conexao", 3000);
  }
  input.disabled = false;
  input.focus();
}

async function removeUpstream(server) {
  const r = await fetch("/api/dns/upstream/" + encodeURIComponent(server), {method: "DELETE"});
  if (r.ok) {
    fetchDNSStatus();
  } else {
    const e = await r.json();
    toast("Erro: " + (e.error || "falha"), 3000);
  }
}

async function toggleDNSExternal() {
  const btn = $("#dns-external");
  const wasExternal = btn.textContent.includes("Fechar");
  const newVal = !wasExternal;
  btn.disabled = true;
  btn.textContent = "aplicando...";
  try {
    const r = await fetch("/api/config", {
      method: "PUT",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({dns: {external_listen: newVal}}),
    });
    if (!r.ok) {
      const e = await r.json();
      toast("Erro: " + (e.error || "falha"), 3000);
      return;
    }
    // Restart CoreDNS via service restart
    await fetch("/api/service/coredns/restart", {method: "POST"});
    toast("DNS " + (newVal ? "aberto" : "fechado") + " para rede externa", 2000);
  } catch {
    toast("Erro de conexao", 3000);
  }
  btn.disabled = false;
  fetchDNSStatus();
}

function siteUrl(host, port, dnsMode) {
  const h = host.replace(/:\d+$/, "");
  const useHost = dnsMode === "system" ? h : "127.0.0.1";
  const base = port === 80 || port === 0
    ? `http://${useHost}/`
    : `http://${useHost}:${port}/`;
  return base;
}

async function doWebRestart() {
  const btn = $("#web-restart");
  if (btn) btn.disabled = true;
  try {
    const r = await fetch("/api/web/restart", { method: "POST" });
    const d = await r.json();
    if (r.ok && d.running) {
      toast(`Servidor web na porta ${d.actual_port}`);
    } else if (r.ok) {
      toast("Web reiniciado mas ainda parado: " + (d.error || "verifique porta 80"), 4000);
    } else {
      toast("Erro: " + (d.error || "falha"), 3000);
    }
    fetchStatus();
    renderSites();
  } catch {
    toast("Erro de conexao", 3000);
  }
  if (btn) btn.disabled = false;
}

async function addSite() {
  const host = $("#site-host").value.trim();
  const type = $("#site-type").value;
  const root = $("#site-root").value.trim();
  const upstream = $("#site-upstream").value.trim();
  if (!host) return;
  if (type === "static" && !root) return;
  if (type === "proxy" && !upstream) return;
  const btn = $("#site-add");
  btn.disabled = true;
  try {
    const payload = {host, type, enabled: true};
    if (type === "static") payload.root = root;
    if (type === "proxy") payload.upstream = upstream;
    const r = await fetch("/api/sites", {
      method: "POST",
      headers: {"Content-Type":"application/json"},
      body: JSON.stringify(payload),
    });
    if (r.ok) {
      $("#site-host").value = "";
      $("#site-root").value = "";
      $("#site-upstream").value = "";
      renderSites();
      toast("Site adicionado!");
    } else {
      const d = await r.json();
      toast("Erro: " + (d.error || "desconhecido"), 3000);
    }
  } catch { toast("Erro de rede", 3000); }
  btn.disabled = false;
}

async function deleteSite(host) {
  if (!confirm(`Remover site ${host}?`)) return;
  try {
    await fetch("/api/sites/" + encodeURIComponent(host), { method: "DELETE" });
    renderSites();
    toast("Site removido");
  } catch { toast("Erro ao remover", 3000); }
}

async function doWebEnable80() {
  const btn = $("#web-enable-80");
  if (!confirm("Ativar porta 80 (cap_net_bind_service)? Ser\u00e1 pedida senha root. O agente ser\u00e1 reiniciado automaticamente.")) return;
  const orig = btn.textContent;
  btn.disabled = true;
  btn.textContent = "aplicando...";
  try {
    const r = await fetch("/api/web/enable-80", { method: "POST" });
    btn.textContent = r.ok ? "reiniciando..." : "erro \u2717";
    btn.style.color = r.ok ? "var(--green)" : "var(--red)";
    if (r.ok) {
      setTimeout(() => location.reload(), 3500);
      return;
    }
  } catch {
    btn.textContent = "erro \u2717";
    btn.style.color = "var(--red)";
  }
  setTimeout(() => { btn.textContent = orig; btn.style.color = ""; btn.disabled = false; }, 3000);
}

async function serviceAction(svc, act) {
  const btn = document.querySelector(`button[data-svc="${svc}"][data-act="${act}"]`);
  if (btn) btn.disabled = true;
  try {
    await fetch(`/api/service/${svc}/${act}`, { method: "POST" });
  } finally {
    setTimeout(() => { if (btn) btn.disabled = false; }, 800);
    fetchStatus();
  }
}

async function doP2PReconnect() {
  const btn = $("#p2p-reconnect");
  if (!btn) return;
  const orig = btn.textContent;
  btn.disabled = true;
  btn.textContent = "conectando…";
  try {
    const r = await fetch("/api/p2p/restart", { method: "POST" });
    const d = await r.json();
    if (!r.ok || !d.ok) throw new Error(d.error || "falha ao reconectar");
    toast(d.message || (d.connected ? "P2P conectado" : "P2P reiniciado"));
    refreshScraperInfo();
  } catch (e) {
    toast("Erro P2P: " + (e.message || e), 4000);
  } finally {
    btn.disabled = false;
    btn.textContent = orig;
  }
}

async function saveConfig() {
  const cfg = { ...(currentConfig || {}) };
  cfg.node = { name: $("#cfg-node-name").value.trim() };
  cfg.yggdrasil = {
    ...cfg.yggdrasil,
    mode: $("#cfg-ygg-mode").value,
    external_binary: $("#cfg-ygg-ext").value.trim(),
    peers: $("#cfg-ygg-peers").value.split("\n").map(s=>s.trim()).filter(Boolean),
  };
  cfg.dns = {
    ...cfg.dns,
    enabled: cfg.dns?.enabled !== false,
    mode: cfg.dns?.mode || "local",
    listen: cfg.dns?.listen || "127.0.0.1",
    port: cfg.dns?.port || 5333,
    upstream: $("#cfg-dns-upstream").value.split(",").map(s=>s.trim()).filter(Boolean),
    search_domains: $("#cfg-dns-tlds").value.split(",").map(s=>s.trim()).filter(Boolean),
  };
  cfg.scraper = {
    ...cfg.scraper,
    enabled: $("#cfg-scraper-enabled").checked,
    max_concurrent: parseInt($("#cfg-scraper-concurrent").value, 10) || 3,
    max_depth: parseInt($("#cfg-scraper-depth").value, 10) || 3,
    default_schedule_days: parseInt($("#cfg-scraper-schedule").value, 10),
  };
  const r = await fetch("/api/config", { method: "PUT", headers: {"Content-Type":"application/json"}, body: JSON.stringify(cfg) });
  if (r.ok) configDirty = false;
  return r.ok;
}

async function doYggPriv() {
  const b = $("#ygg-priv");
  if (!confirm("Conceder privil\u00e9gio de TUN (cap_net_admin) ao Yggdrasil? Ser\u00e1 pedida senha (root).")) return;
  const orig = b.textContent; b.disabled = true; b.textContent = "aplicando...";
  try {
    const r = await fetch("/api/yggdrasil/setup-priv", { method: "POST" });
    b.textContent = r.ok ? "feito \u2713" : "erro \u2717";
    b.style.color = r.ok ? "var(--green)" : "var(--red)";
  } catch { b.textContent = "erro \u2717"; b.style.color = "var(--red)"; }
  setTimeout(() => { b.textContent = orig; b.style.color = ""; b.disabled = false; fetchStatus(); }, 2500);
}

async function doDNS(action) {
  const btn = action === "enable-system" ? $("#dns-enable") : $("#dns-disable");
  if (!confirm(action === "enable-system"
    ? "Ativar DNS no sistema? Ser\u00e1 pedida senha (root) para: setcap no coredns e integra\u00e7\u00e3o do resolvedor."
    : "Desativar DNS do sistema e restaurar configura\u00e7\u00e3o original?")) return;
  btn.disabled = true;
  const orig = btn.textContent;
  btn.textContent = "aplicando...";
  try {
    const r = await fetch("/api/dns/" + action, { method: "POST" });
    btn.textContent = r.ok ? "feito \u2713" : "erro \u2717";
    btn.style.color = r.ok ? "var(--green)" : "var(--red)";
  } catch {
    btn.textContent = "erro \u2717"; btn.style.color = "var(--red)";
  }
  setTimeout(() => { btn.textContent = orig; btn.style.color = ""; fetchStatus(); }, 2500);
}

/* Logs */
function appendLog(e) {
  if (logFilter && e.source !== logFilter) return;
  logLines.push(e);
  if (logLines.length > 1000) logLines.shift();
  const el = document.createElement("span");
  el.className = "log-line " + e.level;
  const t = new Date(e.time).toLocaleTimeString("pt-BR");
  el.innerHTML = `<span class="t">${t}</span> <span class="src">[${e.source}]</span> ${escapeHtml(e.message)}`;
  const pre = $("#log");
  pre.appendChild(el);
  while (pre.children.length > 1000) pre.removeChild(pre.firstChild);
  if ($("#log-autoscroll").checked) pre.scrollTop = pre.scrollHeight;
}

function refreshLogSources() {
  const sel = $("#log-source");
  if (!sel) return;
  const cur = sel.value;
  const seen = new Set();
  logLines.forEach(e => seen.add(e.source));
  const opts = ["", ...Array.from(seen).sort()];
  if (opts.length + "" === sel.options.length + "" && Array.from(sel.options).every((o,i)=>o.value === (opts[i]||""))) return;
  sel.innerHTML = "";
  opts.forEach(v => {
    const o = document.createElement("option");
    o.value = v; o.textContent = v || "todos";
    sel.appendChild(o);
  });
  sel.value = cur;
}

function connectLogs() {
  const es = new EventSource("/api/logs/stream");
  es.onmessage = (ev) => {
    try { appendLog(JSON.parse(ev.data)); } catch {}
  };
  es.onerror = () => { es.close(); setTimeout(connectLogs, 2000); };
}

async function loadRecent() {
  try {
    const r = await fetch("/api/logs/recent?n=200");
    const arr = await r.json();
    arr.forEach(appendLog);
  } catch {}
}

/* Identity */
async function doExportIdentity() {
  try {
    const r = await fetch("/api/yggdrasil/identity");
    const d = await r.json();
    if (d.private_key) {
      $("#ygg-export-key").value = d.private_key;
      openModal("modal-export");
    } else {
      toast("Erro: identidade n\u00e3o dispon\u00edvel", 3000);
    }
  } catch {
    toast("Erro ao exportar identidade", 3000);
  }
}

async function doImportIdentity() {
  const key = $("#ygg-import-key").value.trim();
  if (!key) { toast("Cole a chave privada primeiro"); return; }
  if (!confirm("Importar nova identidade? O Yggdrasil ser\u00e1 reiniciado e o endere\u00e7o IPv6 ser\u00e1 alterado. Os sites .bl que usam o endere\u00e7o antigo ficar\u00e3o inacess\u00edveis at\u00e9 apontarem para o novo.")) return;
  try {
    const r = await fetch("/api/yggdrasil/identity", {
      method: "POST",
      headers: {"Content-Type":"application/json"},
      body: JSON.stringify({ private_key: key }),
    });
    if (r.ok) {
      toast("Identidade importada! Yggdrasil reiniciando...", 3000);
      closeModal("modal-import");
      setTimeout(fetchStatus, 3000);
    } else {
      const d = await r.json();
      toast("Erro: " + (d.error || "desconhecido"), 3000);
    }
  } catch {
    toast("Erro de rede", 3000);
  }
}

/* Event delegation */
document.addEventListener("click", (ev) => {
  const b = ev.target.closest("button[data-svc]");
  if (b) { serviceAction(b.dataset.svc, b.dataset.act); return; }
  if (ev.target.id === "dns-enable") doDNS("enable-system");
  if (ev.target.id === "dns-disable") doDNS("disable-system");
  if (ev.target.id === "cfg-save") {
    saveConfig().then(ok => {
      const btn = $("#cfg-save");
      const orig = btn.textContent;
      btn.textContent = ok ? "salvo \u2713" : "erro \u2717";
      btn.style.color = ok ? "var(--green)" : "var(--red)";
      if (ok) toast("Configura\u00e7\u00e3o salva!");
      setTimeout(() => { btn.textContent = orig; btn.style.color = ""; }, 1500);
      fetchStatus();
    });
  }
  if (ev.target.id === "log-clear") { $("#log").innerHTML = ""; logLines.length = 0; }
  if (ev.target.id === "ygg-priv") doYggPriv();
  if (ev.target.id === "ygg-export") doExportIdentity();
  if (ev.target.id === "ygg-import") openModal("modal-import");
  if (ev.target.id === "ygg-import-confirm") doImportIdentity();
  if (ev.target.id === "couch-copy-pass") {
    const pass = $("#couch-admin-pass")?.dataset.secret;
    if (pass) copyText(pass);
    return;
  }
  if (ev.target.id === "couch-regen-pass") {
    regenerateCouchPassword();
    return;
  }
  if (ev.target.id === "ygg-copy-btn" && yggAddr) copyText(yggAddr);
  if (ev.target.id === "ygg-export-copy") {
    const key = $("#ygg-export-key").value;
    if (key) copyText(key);
  }
  if (ev.target.id === "site-add") addSite();
  if (ev.target.id === "web-enable-80") doWebEnable80();
  if (ev.target.id === "web-restart") doWebRestart();
  if (ev.target.id === "scraper-add-url") addScraperURL();
  if (ev.target.id === "scraper-clear-queue") clearScraperQueue();
  if (ev.target.id === "scraper-refresh-results") refreshScraperInfo();
  if (ev.target.id === "p2p-reconnect") doP2PReconnect();
  if (ev.target.id === "p2p-cfg-save-simple") saveP2PConfigSimple();
  if (ev.target.id === "p2p-cfg-save-advanced") saveP2PConfigAdvanced();
  if (ev.target.id === "p2p-cfg-reset") resetP2PConfig();
  if (ev.target.id === "p2p-cfg-reload") loadP2PConfig(true);
  if (ev.target.id === "scraper-cfg-save") {
    saveConfig().then(ok => {
      const btn = $("#scraper-cfg-save");
      const orig = btn.textContent;
      btn.textContent = ok ? "salvo \u2713" : "erro \u2717";
      btn.style.color = ok ? "var(--green)" : "var(--red)";
      if (ok) toast("Configura\u00e7\u00e3o do scraper salva!");
      setTimeout(() => { btn.textContent = orig; btn.style.color = ""; }, 1500);
      fetchStatus();
    });
  }
  if (ev.target.id === "autostart-disable") doAutostart(false);
  if (ev.target.id === "upd-check") doUpdateCheck();
  if (ev.target.id === "upd-download") doUpdateDownload();
  if (ev.target.id === "upd-install") doUpdateInstall();
  if (ev.target.id === "update-banner-install") doUpdateInstall();
  if (ev.target.id === "update-banner-dismiss") {
    updateDismissed = true;
    $("#update-banner").style.display = "none";
  }
});

$("#log-source")?.addEventListener("change", (e) => {
  logFilter = e.target.value;
  $("#log").innerHTML = "";
  logLines.filter(l => !logFilter || l.source === logFilter).forEach(l => appendLog(l));
});

$("#site-type")?.addEventListener("change", (e) => {
  const type = e.target.value;
  $("#site-root").style.display = type === "static" ? "block" : "none";
  $("#site-upstream").style.display = type === "proxy" ? "block" : "none";
});

const updateStateLabel = (st) => ({
  idle: "aguardando",
  checking: "verificando…",
  downloading: "baixando…",
  ready: "pronto para instalar",
  installing: "instalando…",
  done: "concluído",
  error: "erro",
}[st] || st);

function renderUpdateUI(u) {
  if (!u || updateBusy) return;
  lastUpdateStatus = u;
  const cur = $("#upd-current");
  if (cur) cur.textContent = u.current || "—";
  const lc = $("#upd-last-check");
  if (lc) lc.textContent = u.last_check ? new Date(u.last_check).toLocaleString() : "—";
  const st = $("#upd-state");
  if (st) st.textContent = updateStateLabel(u.state);
  const progWrap = $("#upd-progress-wrap");
  const prog = $("#upd-progress");
  if (progWrap && prog) {
    const show = u.state === "downloading" || (u.progress > 0 && u.progress < 100);
    progWrap.style.display = show ? "flex" : "none";
    prog.textContent = `${u.progress || 0}%`;
  }
  const msg = $("#upd-message");
  if (msg) {
    if (u.error) {
      msg.textContent = u.error;
      msg.style.color = "var(--red)";
    } else if (u.available) {
      msg.textContent = `Nova versão ${u.latest} disponível.`;
      msg.style.color = "var(--amber)";
    } else if (u.state === "done") {
      msg.textContent = "Atualização instalada. Reinicie o app desktop se necessário.";
      msg.style.color = "var(--green)";
    } else {
      msg.textContent = "Você está na versão mais recente.";
      msg.style.color = "";
    }
  }
  const notes = $("#upd-notes");
  if (notes) {
    if (u.notes && u.available) {
      notes.textContent = u.notes;
      notes.style.display = "block";
    } else {
      notes.style.display = "none";
    }
  }
  const portable = $("#upd-portable-hint");
  const link = $("#upd-release-link");
  if (portable) {
    const showPortable = u.available && !u.can_install;
    portable.style.display = showPortable ? "block" : "none";
    if (link && u.release_url) link.href = u.release_url;
  }
  const btnCheck = $("#upd-check");
  const btnDl = $("#upd-download");
  const btnInst = $("#upd-install");
  const busy = ["checking", "downloading", "installing"].includes(u.state);
  if (btnCheck) btnCheck.disabled = busy;
  if (btnDl) btnDl.disabled = busy || !u.available || !u.can_install || u.state === "ready";
  if (btnInst) btnInst.disabled = busy || !u.available || !u.can_install;
  const banner = $("#update-banner");
  const bannerVer = $("#update-banner-ver");
  if (banner && bannerVer) {
    const showBanner = u.available && !updateDismissed && u.state !== "installing" && u.state !== "done";
    banner.style.display = showBanner ? "flex" : "none";
    if (showBanner) bannerVer.textContent = u.latest || "";
  }
}

async function refreshUpdateStatus() {
  if (updateBusy) return;
  try {
    const r = await fetch("/api/update/status");
    if (!r.ok) return;
    const u = await r.json();
    renderUpdateUI(u);
  } catch {
    // painel pode estar sem updater em builds antigos
  }
}

async function doUpdateCheck() {
  updateBusy = true;
  renderUpdateUI({ ...(lastUpdateStatus || {}), state: "checking" });
  try {
    const r = await fetch("/api/update/check", { method: "POST" });
    const u = await r.json();
    updateBusy = false;
    renderUpdateUI(u);
    if (u.error) toast(u.error);
    else toast(u.available ? `Versão ${u.latest} disponível` : "Sem atualizações");
  } catch (e) {
    updateBusy = false;
    toast("Erro ao verificar atualizações");
  }
}

async function doUpdateDownload() {
  updateBusy = true;
  renderUpdateUI({ ...(lastUpdateStatus || {}), state: "downloading", progress: 0 });
  try {
    const r = await fetch("/api/update/download", { method: "POST" });
    const u = await r.json();
    updateBusy = false;
    if (!r.ok) {
      toast(u.error || "Falha no download");
      await refreshUpdateStatus();
      return;
    }
    renderUpdateUI(u);
    toast("Pacote baixado");
  } catch {
    updateBusy = false;
    toast("Erro no download");
  }
}

async function doUpdateInstall() {
  if (!confirm("Instalar atualização agora? Será solicitada sua senha de administrador.")) return;
  updateBusy = true;
  renderUpdateUI({ ...(lastUpdateStatus || {}), state: "installing" });
  try {
    const r = await fetch("/api/update/install", { method: "POST" });
    const u = await r.json();
    updateBusy = false;
    if (!r.ok) {
      toast(u.error || "Falha na instalação");
      await refreshUpdateStatus();
      return;
    }
    renderUpdateUI(u);
    toast("Atualização instalada — reiniciando serviços…");
    setTimeout(fetchStatus, 3000);
  } catch {
    updateBusy = false;
    toast("Erro na instalação");
  }
}

fetchStatus();
fetchDNSStatus();
document.querySelectorAll("[id^='cfg-']").forEach(el => {
  el.addEventListener("input", markConfigDirty);
  el.addEventListener("change", markConfigDirty);
});
["p2p-simple-enabled", "p2p-simple-urls", "p2p-simple-max-results", "p2p-config-yaml"].forEach(id => {
  const el = document.getElementById(id);
  if (!el) return;
  el.addEventListener("input", markP2PConfigDirty);
  el.addEventListener("change", markP2PConfigDirty);
});
setInterval(fetchStatus, 3000);
setInterval(refreshUpdateStatus, 5000);
setInterval(fetchDNSStatus, 5000);
loadRecent().then(connectLogs);

$("#dns-upstream-add").addEventListener("click", addUpstream);
$("#dns-upstream-input").addEventListener("keydown", e => { if (e.key === "Enter") addUpstream(); });
$("#dns-external").addEventListener("click", toggleDNSExternal);
$("#dns-ygg-copy").addEventListener("click", () => {
  const addr = $("#dns-ygg-addr").textContent;
  if (addr) copyText(addr);
});
