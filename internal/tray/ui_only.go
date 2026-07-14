package tray

import (
	"net/http"
	"time"

	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/openurl"

	"fyne.io/systray"
)

// RunUIOnly shows a lightweight tray that talks to an already-running Agent API.
// Quit only exits this process — it does not stop the Windows service / daemon.
func RunUIOnly(panelURL string, buf *logx.Buffer) {
	if panelURL == "" {
		panelURL = "http://127.0.0.1:9970"
	}
	if buf == nil {
		buf = logx.NewBuffer(200)
	}
	t := &uiOnly{panelURL: panelURL, buf: buf}
	systray.Run(t.onReady, func() {})
}

type uiOnly struct {
	panelURL string
	buf      *logx.Buffer
}

func (t *uiOnly) onReady() {
	systray.SetIcon(iconBytes(false))
	systray.SetTitle("BuscaLogo")
	systray.SetTooltip("BuscaLogo Agent (UI)")

	mOpen := systray.AddMenuItem("Abrir painel", "Abre o painel no navegador")
	mStatus := systray.AddMenuItem("Estado: …", "")
	mStatus.Disable()
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Fechar bandeja", "Fecha só esta UI — o serviço continua")

	go t.pollStatus(mStatus)

	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				if err := openurl.Open(t.panelURL); err != nil {
					t.buf.Warnf("tray-ui", "abrir painel: %v", err)
				}
			case <-mQuit.ClickedCh:
				t.buf.Infof("tray-ui", "fechando UI (serviço intacto)")
				systray.Quit()
				return
			}
		}
	}()

	go func() {
		time.Sleep(400 * time.Millisecond)
		if apiReachable(t.panelURL) {
			_ = openurl.Open(t.panelURL)
		} else {
			t.buf.Warnf("tray-ui", "serviço não responde em %s — inicie o serviço BuscaLogoAgent", t.panelURL)
		}
	}()
}

func (t *uiOnly) pollStatus(item *systray.MenuItem) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		ok := apiReachable(t.panelURL)
		if ok {
			item.SetTitle("Estado: serviço online")
			systray.SetIcon(iconBytes(true))
		} else {
			item.SetTitle("Estado: serviço parado")
			systray.SetIcon(iconBytes(false))
		}
		<-tick.C
	}
}

func apiReachable(panelURL string) bool {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(panelURL + "/api/status")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}
