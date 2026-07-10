if (typeof chrome === "undefined" && typeof browser !== "undefined") { globalThis.chrome = browser; }
(function () {
  const ID = "buscalogo-agent-chip";

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
    el.onclick = null;

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
        const res = await chrome.runtime.sendMessage({ type: "BL_SUGGEST", url });
        if (!res?.ok) throw new Error(res?.error || "falha");
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

  chrome.runtime.onMessage.addListener((msg) => {
    if (msg?.type === "BL_LOOKUP") {
      render(msg.result || {}, msg.url || location.href);
    }
    if (msg?.type === "BL_HIDE_ALERT") {
      hideChip();
    }
  });

  chrome.runtime
    .sendMessage({ type: "BL_LOOKUP_REQUEST", url: location.href })
    .then((result) => {
      if (result) render(result, location.href);
    })
    .catch(() => {});
})();
