/**
 * BuscaLogo Agent — app único (Neutralino + daemon interno buscalogo-agentd)
 */

const PANEL_URL = "http://127.0.0.1:9970";
const API_STATUS = `${PANEL_URL}/api/status`;
const API_SHUTDOWN = `${PANEL_URL}/api/shutdown`;
const DAEMON_NAME = "buscalogo-agentd";
const IS_WIN = typeof NL_OS !== "undefined" && NL_OS === "Windows";

function isNeuDevRun() {
  return Boolean(globalThis.NL_GINJECTED || globalThis.NL_CINJECTED);
}

let agentSpawned = false;
let agentProc = null;

function setStatus(msg) {
  const el = document.getElementById("status");
  if (el) el.textContent = msg;
}

function showError(msg) {
  const err = document.getElementById("error");
  const retry = document.getElementById("retry");
  if (err) {
    err.textContent = msg;
    err.hidden = false;
  }
  if (retry) retry.hidden = false;
  const spinner = document.querySelector(".spinner");
  if (spinner) spinner.style.display = "none";
}

function daemonCandidates() {
  const names = IS_WIN ? [`${DAEMON_NAME}.exe`, DAEMON_NAME] : [DAEMON_NAME];
  const dirs = [NL_PATH, `${NL_PATH}/..`, "/opt/buscalogo"];
  const out = [];
  for (const dir of dirs) {
    for (const name of names) {
      out.push(`${dir}/${name}`);
    }
  }
  return out;
}

async function resolveDaemonBinary() {
  for (const path of daemonCandidates()) {
    try {
      const info = await Neutralino.filesystem.getStats(path);
      if (info && !info.isDirectory) return path;
    } catch {
      // tenta próximo
    }
  }
  try {
    const checks = daemonCandidates().map((p) => `[ -x "${p}" ] && echo "${p}"`).join("; ");
    const r = await Neutralino.os.execCommand(`sh -c '${checks}; true'`);
    const line = (r.stdOut || "").trim().split("\n").find((l) => l.includes(DAEMON_NAME));
    if (line) return line.trim();
  } catch {
    // ignore
  }
  return null;
}

function trayMenuItems() {
  return [
    { id: "PANEL", text: "Abrir painel" },
    { id: "BROWSER", text: "Abrir no navegador" },
    { id: "SEP", text: "-" },
    { id: "QUIT", text: "Sair" },
  ];
}

async function trayLog(msg, level = "INFO") {
  const line = `[systray] ${msg}`;
  console.log(line);
  try {
    await Neutralino.debug.log(line, level);
  } catch {
    // pode rodar antes do WebSocket estar pronto
  }
}

// Linux em produção (resources.neu): setTray só aceita paths do bundle (/resources/...).
// Caminhos absolutos (/opt/...) são ignorados — o Neutralino chama extractFile no manifest.
const TRAY_ICON = "/resources/icons/trayIcon.png";

async function setTray() {
  if (NL_MODE !== "window" || NL_OS === "Darwin") {
    await trayLog(`ignorado (mode=${NL_MODE}, os=${NL_OS})`);
    return;
  }

  try {
    const gdk = await Neutralino.os.getEnv("GDK_BACKEND");
    await trayLog(
      `GDK_BACKEND=${gdk || "(unset)"} icon=${TRAY_ICON} NL_PATH=${NL_PATH} NL_DATAPATH=${NL_DATAPATH}`
    );
  } catch {
    await trayLog(`início icon=${TRAY_ICON} NL_PATH=${NL_PATH} NL_DATAPATH=${NL_DATAPATH}`);
  }

  try {
    const s = await Neutralino.resources.getStats(TRAY_ICON);
    await trayLog(`${TRAY_ICON} → bundle OK (${s.size} bytes)`);
  } catch {
    await trayLog(`${TRAY_ICON} → ausente no resources.neu`, "WARN");
  }

  try {
    await Neutralino.os.setTray({ icon: TRAY_ICON, menuItems: trayMenuItems() });
    await trayLog(`OK — systray via ${TRAY_ICON}`);
  } catch (e) {
    await trayLog(`setTray falhou: ${e?.message || e}`, "ERROR");
  }
}

async function isAgentRunning() {
  try {
    const ctrl = new AbortController();
    const t = setTimeout(() => ctrl.abort(), 2000);
    const r = await fetch(API_STATUS, { signal: ctrl.signal });
    clearTimeout(t);
    return r.ok;
  } catch {
    return false;
  }
}

async function waitForAgent(maxMs = 45000) {
  const start = Date.now();
  while (Date.now() - start < maxMs) {
    if (await isAgentRunning()) return true;
    await new Promise((r) => setTimeout(r, 500));
  }
  return false;
}

async function startAgent() {
  if (await isAgentRunning()) {
    setStatus("Serviços ativos");
    return true;
  }

  const binary = await resolveDaemonBinary();
  if (!binary) {
    showError(
      `${DAEMON_NAME} não encontrado. Reinstale o pacote ou rode make build na raiz do projeto.`
    );
    return false;
  }

  setStatus("Iniciando serviços…");
  try {
    const cwd = binary.includes("/") ? binary.replace(/[/\\][^/\\]+$/, "") : NL_PATH;
    agentProc = await Neutralino.os.spawnProcess(`${binary} --no-tray`, { cwd });
    agentSpawned = true;
  } catch (e) {
    showError(`Falha ao iniciar: ${e.message || e}`);
    return false;
  }

  setStatus("Aguardando painel…");
  const ok = await waitForAgent();
  if (!ok) {
    showError("O painel não respondeu em http://127.0.0.1:9970.");
    return false;
  }
  return true;
}

async function openPanel() {
  setStatus("Abrindo painel…");
  const splash = document.getElementById("splash");
  const panel = document.getElementById("panel");
  if (panel) {
    panel.src = PANEL_URL;
    panel.hidden = false;
  }
  if (splash) splash.hidden = true;
}

async function boot() {
  document.getElementById("retry")?.addEventListener("click", () => {
    document.getElementById("error").hidden = true;
    document.getElementById("retry").hidden = true;
    document.querySelector(".spinner").style.display = "block";
    boot();
  });

  const ok = await startAgent();
  if (ok) await openPanel();
}

function withTimeout(promise, ms) {
  return Promise.race([
    promise,
    new Promise((_, reject) => setTimeout(() => reject(new Error("timeout")), ms)),
  ]);
}

async function stopDaemon() {
  try {
    const ctrl = new AbortController();
    const t = setTimeout(() => ctrl.abort(), 2500);
    const r = await fetch(API_SHUTDOWN, { method: "POST", signal: ctrl.signal });
    clearTimeout(t);
    if (r.ok) {
      await new Promise((resolve) => setTimeout(resolve, 600));
      return;
    }
  } catch {
    // tenta fallbacks abaixo
  }

  if (agentSpawned && agentProc?.id != null) {
    try {
      await withTimeout(Neutralino.os.updateSpawnedProcess(agentProc.id, "exit"), 4000);
      return;
    } catch {
      // força kill pelo pid
    }
  }

  const pids = new Set();
  if (agentProc?.pid) pids.add(agentProc.pid);
  try {
    const procs = await Neutralino.os.getSpawnedProcesses();
    for (const p of procs) {
      if (p.pid) pids.add(p.pid);
    }
  } catch {
    // ignore
  }
  for (const pid of pids) {
    try {
      await Neutralino.os.execCommand(`kill -TERM ${pid} 2>/dev/null || true`);
    } catch {
      // ignore
    }
  }
  try {
    await Neutralino.os.execCommand(
      "pkill -TERM -f 'buscalogo-agentd.*--no-tray' 2>/dev/null || true"
    );
  } catch {
    // ignore
  }
}

async function shutdown() {
  try {
    await stopDaemon();
  } catch (e) {
    console.error("stopDaemon:", e);
  }
  try {
    await Neutralino.app.exit(0);
  } catch (e) {
    console.error("app.exit:", e);
    try {
      await Neutralino.app.killProcess();
    } catch {
      // ignore
    }
  }
}

async function onTrayMenuItemClicked(event) {
  const id = event?.detail?.id;
  if (!id) return;

  switch (id) {
    case "PANEL":
      await Neutralino.window.show();
      await Neutralino.window.focus();
      if (!(await isAgentRunning())) await startAgent();
      await openPanel();
      break;
    case "BROWSER":
      await Neutralino.os.open(PANEL_URL);
      break;
    case "QUIT":
      await shutdown();
      break;
  }
}

async function onWindowClose() {
  await Neutralino.window.hide();
}

Neutralino.init();
Neutralino.events.on("trayMenuItemClicked", onTrayMenuItemClicked);
Neutralino.events.on("windowClose", onWindowClose);

// Igual ao app Neutralino de referência: setTray síncrono logo após init (não no ready).
if (typeof NL_OS !== "undefined" && NL_OS !== "Darwin") {
  setTray();
}

Neutralino.events.on("ready", () => {
  boot();
  setInterval(checkUpdateRestart, 5000);
});

async function checkUpdateRestart() {
  try {
    const ctrl = new AbortController();
    const t = setTimeout(() => ctrl.abort(), 2500);
    const r = await fetch(`${PANEL_URL}/api/update/status`, { signal: ctrl.signal });
    clearTimeout(t);
    if (!r.ok) return;
    const d = await r.json();
    if (d.needs_restart) await relaunchApp();
  } catch {
    // painel pode estar reiniciando
  }
}

async function relaunchApp() {
  try {
    await fetch(`${PANEL_URL}/api/update/restart-app`, { method: "POST" });
  } catch {
    // ignore
  }
  const launch = "/opt/buscalogo/launch.sh";
  try {
    await Neutralino.os.execCommand(`nohup sh -c 'sleep 1; ${launch}' >/dev/null 2>&1 &`);
  } catch (e) {
    console.error("relaunch:", e);
  }
  try {
    await Neutralino.app.exit(0);
  } catch {
    try { await Neutralino.app.killProcess(); } catch { /* ignore */ }
  }
}
