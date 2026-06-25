package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"buscalogo-agent/assets"
	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/coredns"
	"buscalogo-agent/internal/couchdb"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/sites"
	"buscalogo-agent/internal/yggdrasil"

	"fyne.io/systray"
)

type Tray struct {
	panelURL    string
	buf         *logx.Buffer
	cfg         *config.Config
	coredns     *coredns.Service
	ygg         *yggdrasil.Service
	couchdb     *couchdb.Service
	sites       *sites.Manager
	onQuit      func()
	siteMenusMu      sync.Mutex
	siteMenus        map[string]*systray.MenuItem
	lastEnabledHosts []string
}

// EnvInfo descreve o ambiente de systray detectado.
type EnvInfo struct {
	OK      bool   `json:"ok"`
	Desktop string `json:"desktop,omitempty"`
	Warning string `json:"warning,omitempty"`
	Details string `json:"details,omitempty"`
}

func New(panelURL string, buf *logx.Buffer, cfg *config.Config, cdns *coredns.Service, y *yggdrasil.Service, cdb *couchdb.Service, sm *sites.Manager, onQuit func()) *Tray {
	return &Tray{panelURL: panelURL, buf: buf, cfg: cfg, coredns: cdns, ygg: y, couchdb: cdb, sites: sm, onQuit: onQuit}
}

// CheckEnvironment verifica se o ambiente Linux tem suporte a systray/appindicator.
func CheckEnvironment() EnvInfo {
	if runtime.GOOS != "linux" {
		return EnvInfo{OK: true, Details: "systray verificado apenas no Linux"}
	}

	desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP"))
	if desktop == "" {
		desktop = strings.ToLower(os.Getenv("DESKTOP_SESSION"))
	}

	info := EnvInfo{
		Desktop: desktop,
		OK:      true,
	}

	// Verifica bibliotecas de appindicator comumente necessárias
	libs := []string{
		"/usr/lib/x86_64-linux-gnu/libappindicator3.so.1",
		"/usr/lib/x86_64-linux-gnu/libayatana-appindicator3.so.1",
		"/usr/lib64/libappindicator3.so.1",
		"/usr/lib64/libayatana-appindicator3.so.1",
	}
	hasLib := false
	var found []string
	for _, l := range libs {
		if _, err := os.Stat(l); err == nil {
			hasLib = true
			found = append(found, l)
		}
	}

	// Verifica binários/pacotes auxiliares
	apps := map[string]string{
		"appindicator3-validate": "validador appindicator",
	}
	for bin, name := range apps {
		if _, err := exec.LookPath(bin); err == nil {
			info.Details += name + " encontrado; "
		}
	}

	if !hasLib {
		info.OK = false
		info.Warning = "biblioteca libappindicator3/libayatana-appindicator3 não encontrada"
		info.Details += "instale o pacote de appindicator para sua distro (ex: libappindicator3-1, libayatana-appindicator3-1, gnome-shell-extension-appindicator)"
		return info
	}

	// Ambientes específicos
	switch {
	case strings.Contains(desktop, "gnome"):
		info.Details += "GNOME detectado. Verifique se a extensão 'AppIndicator and KStatusNotifierItem Support' está habilitada."
	case strings.Contains(desktop, "kde"):
		info.Details += "KDE detectado. System tray geralmente funciona nativamente."
	case strings.Contains(desktop, "xfce"):
		info.Details += "Xfce detectado. System tray geralmente funciona nativamente."
	case strings.Contains(desktop, "unity"):
		info.Details += "Unity detectado. AppIndicator funciona nativamente."
	case strings.Contains(desktop, "cinnamon"):
		info.Details += "Cinnamon detectado. System tray geralmente funciona nativamente."
	case strings.Contains(desktop, "mate"):
		info.Details += "MATE detectado. System tray geralmente funciona nativamente."
	default:
		info.Details += "desktop não identificado; systray depende do suporte AppIndicator/KStatusNotifierItem"
	}

	return info
}

func (t *Tray) Run() {
	defer func() {
		if r := recover(); r != nil {
			t.buf.Errorf("tray", "systray falhou (panic): %v", r)
		}
	}()
	systray.Run(t.onReady, t.onExit)
}

func (t *Tray) onReady() {
	systray.SetIcon(iconBytes(false))
	systray.SetTitle("BuscaLogo")
	systray.SetTooltip("BuscaLogo Agent")

	mOpen := systray.AddMenuItem("Abrir painel ("+t.panelURL+")", "Abre o painel no navegador")
	systray.AddSeparator()
	mCore := systray.AddMenuItem("CoreDNS: …", "Estado do CoreDNS")
	mCore.Disable()
	mYgg := systray.AddMenuItem("Yggdrasil: …", "Estado do Yggdrasil")
	mYgg.Disable()
	mCouch := systray.AddMenuItem("CouchDB: …", "Estado do CouchDB")
	mCouch.Disable()
	systray.AddSeparator()
	mSites := systray.AddMenuItem("Sites", "Sites hospedados neste agente")
	t.siteMenus = make(map[string]*systray.MenuItem)
	systray.AddSeparator()
	mRC := systray.AddMenuItem("Reiniciar CoreDNS", "")
	mRY := systray.AddMenuItem("Reiniciar Yggdrasil", "")
	mRCouch := systray.AddMenuItem("Reiniciar CouchDB", "")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Sair", "Encerra o BuscaLogo Agent")

	go t.refreshLoop(mCore, mYgg, mCouch, mSites)

	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				openBrowser(t.panelURL)
			case <-mRC.ClickedCh:
				t.buf.Infof("tray", "reiniciando CoreDNS via systray")
				go func() { _ = t.coredns.Restart() }()
			case <-mRY.ClickedCh:
				t.buf.Infof("tray", "reiniciando Yggdrasil via systray")
				go func() { _ = t.ygg.Restart() }()
			case <-mRCouch.ClickedCh:
				t.buf.Infof("tray", "reiniciando CouchDB via systray")
				go func() { _ = t.couchdb.Restart() }()
			case <-mQuit.ClickedCh:
				t.buf.Infof("tray", "encerrando agente")
				systray.Quit()
				return
			}
		}
	}()
}

func (t *Tray) onExit() {
	if t.onQuit != nil {
		t.onQuit()
	}
}

func (t *Tray) refreshLoop(mCore, mYgg, mCouch, mSites *systray.MenuItem) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	var lastRunning *bool
	for {
		cs, ys, cds := t.coredns.Status(), t.ygg.Status(), t.couchdb.Status()
		mCore.SetTitle("CoreDNS: " + label(string(cs.State)))
		mYgg.SetTitle("Yggdrasil: " + label(string(ys.State)))
		mCouch.SetTitle("CouchDB: " + label(string(cds.State)))
		running := cs.State == "running" && ys.State == "running"
		if t.cfg.CouchDB.Enabled {
			running = running && cds.State == "running"
		}
		if lastRunning == nil || *lastRunning != running {
			systray.SetIcon(iconBytes(running))
			lastRunning = &running
		}
		t.refreshSitesMenu(mSites)
		<-tick.C
	}
}

// refreshSitesMenu mantém o submenu "Sites" sincronizado com os sites habilitados.
// Evita re-renderizações desnecessárias para não fechar o menu aberto do usuário.
func (t *Tray) refreshSitesMenu(parent *systray.MenuItem) {
	if t.sites == nil {
		return
	}
	list := t.sites.ListSites()

	t.siteMenusMu.Lock()
	defer t.siteMenusMu.Unlock()

	// Verifica se houve mudança real na lista de sites habilitados.
	enabledHosts := make([]string, 0, len(list))
	for _, s := range list {
		if s.Enabled {
			enabledHosts = append(enabledHosts, s.Host)
		}
	}
	if sameStringSet(enabledHosts, t.lastEnabledHosts) {
		return
	}
	t.lastEnabledHosts = enabledHosts

	// Remove itens de sites desabilitados ou removidos.
	for host, item := range t.siteMenus {
		if host == "__empty__" {
			continue
		}
		keep := false
		for _, s := range list {
			if s.Host == host && s.Enabled {
				keep = true
				break
			}
		}
		if !keep {
			item.Hide()
			delete(t.siteMenus, host)
		}
	}

	hasEnabled := len(enabledHosts) > 0
	for _, s := range list {
		if !s.Enabled {
			continue
		}
		item, ok := t.siteMenus[s.Host]
		if !ok {
			item = parent.AddSubMenuItem(s.Host, "Abre "+s.Host+" no navegador")
			t.siteMenus[s.Host] = item
			url := t.siteURL(s.Host)
			go t.siteClickHandler(item, url)
		}
		item.SetTitle(s.Host)
		item.Show()
	}

	if !hasEnabled {
		if item, ok := t.siteMenus["__empty__"]; !ok {
			item = parent.AddSubMenuItem("Nenhum site habilitado", "")
			item.Disable()
			t.siteMenus["__empty__"] = item
		} else {
			item.Show()
		}
	} else if item, ok := t.siteMenus["__empty__"]; ok {
		item.Hide()
	}
}

func (t *Tray) siteClickHandler(item *systray.MenuItem, url string) {
	for range item.ClickedCh {
		t.buf.Infof("tray", "abrindo site via systray: %s", url)
		openBrowser(url)
	}
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]int, len(a))
	for _, v := range a {
		m[v]++
	}
	for _, v := range b {
		if m[v] == 0 {
			return false
		}
		m[v]--
	}
	return true
}

// siteURL gera o link de acesso a um site, considerando modo DNS e porta efetiva.
// No Modo A (local) usa 127.0.0.1 porque o navegador não resolve .bl via CoreDNS :5333.
func (t *Tray) siteURL(host string) string {
	port := t.sites.ActualPort()
	if t.cfg != nil && t.cfg.DNS.Mode == "system" {
		if port == 80 {
			return "http://" + host + "/"
		}
		return "http://" + host + ":" + strconv.Itoa(port) + "/"
	}
	if port == 80 {
		return "http://127.0.0.1/"
	}
	return "http://127.0.0.1:" + strconv.Itoa(port) + "/"
}

func label(s string) string {
	switch s {
	case "running":
		return "rodando"
	case "stopped":
		return "parado"
	case "crashed":
		return "caiu"
	case "starting":
		return "iniciando"
	case "disabled":
		return "desabilitado"
	}
	return "?"
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("open", url)
	}
	_ = cmd.Start()
}

const iconSize = 32

func iconBytes(running bool) []byte {
	logo := assets.Logo()
	if len(logo) > 0 {
		if img, _, err := image.Decode(bytes.NewReader(logo)); err == nil {
			scaled := resizeIcon(cropCenterSquare(img), iconSize)
			return overlayStatusDot(scaled, running)
		}
	}
	// fallback: ícone gerado (B de BuscaLogo + status)
	return generatedIcon(running)
}

// cropCenterSquare corta a região central quadrada da imagem (zoom no rosto/logo).
func cropCenterSquare(src image.Image) image.Image {
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w == h {
		return src
	}
	size := min(w, h)
	x0 := bounds.Min.X + (w-size)/2
	y0 := bounds.Min.Y + (h-size)/2
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), src, image.Point{X: x0, Y: y0}, draw.Src)
	return img
}

// resizeIcon redimensiona a imagem para size x size.
func resizeIcon(src image.Image, size int) image.Image {
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w == size && h == size {
		return src
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx := float64(x) * float64(w) / float64(size)
			fy := float64(y) * float64(h) / float64(size)
			img.Set(x, y, sample(src, fx, fy))
		}
	}
	return img
}

func sample(src image.Image, sx, sy float64) color.Color {
	bounds := src.Bounds()
	x0, y0 := int(sx), int(sy)
	x1, y1 := x0+1, y0+1
	if x1 >= bounds.Max.X { x1 = bounds.Max.X - 1 }
	if y1 >= bounds.Max.Y { y1 = bounds.Max.Y - 1 }
	if x0 < bounds.Min.X { x0 = bounds.Min.X }
	if y0 < bounds.Min.Y { y0 = bounds.Min.Y }

	dx := sx - float64(x0)
	dy := sy - float64(y0)

	c00 := rgba(src.At(x0, y0))
	c10 := rgba(src.At(x1, y0))
	c01 := rgba(src.At(x0, y1))
	c11 := rgba(src.At(x1, y1))

	return color.RGBA{
		R: lerp(c00.R, c10.R, c01.R, c11.R, dx, dy),
		G: lerp(c00.G, c10.G, c01.G, c11.G, dx, dy),
		B: lerp(c00.B, c10.B, c01.B, c11.B, dx, dy),
		A: lerp(c00.A, c10.A, c01.A, c11.A, dx, dy),
	}
}

func rgba(c color.Color) color.RGBA {
	r, g, b, a := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}

func lerp(c00, c10, c01, c11 uint8, dx, dy float64) uint8 {
	v0 := float64(c00)*(1-dx) + float64(c10)*dx
	v1 := float64(c01)*(1-dx) + float64(c11)*dx
	v := v0*(1-dy) + v1*dy
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func overlayStatusDot(src image.Image, running bool) []byte {
	bounds := src.Bounds()
	size := bounds.Dx()
	img := image.NewRGBA(bounds)

	// fundo circular para destacar em trays claros/escuros
	bg := color.RGBA{0x17, 0x1a, 0x21, 0xff}
	cx, cy := float64(size)/2, float64(size)/2
	r := float64(size) / 2
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dx := float64(x) + 0.5 - cx
			dy := float64(y) + 0.5 - cy
			if dx*dx+dy*dy <= r*r {
				img.Set(x, y, bg)
			}
		}
	}

	// desenha a logo redimensionada por cima
	draw.Draw(img, bounds, src, bounds.Min, draw.Over)

	// ponto de status no canto inferior direito
	dot := color.RGBA{0x8b, 0x93, 0xa3, 0xff}
	if running {
		dot = color.RGBA{0x2f, 0xbf, 0x71, 0xff}
	}
	dr := size / 8
	if dr < 3 {
		dr = 3
	}
	dcx, dcy := float64(size-dr-2), float64(size-dr-2)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dx := float64(x) + 0.5 - dcx
			dy := float64(y) + 0.5 - dcy
			if dx*dx+dy*dy <= float64(dr*dr) {
				img.Set(x, y, dot)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func generatedIcon(running bool) []byte {
	size := iconSize
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	bg := color.RGBA{0x17, 0x1a, 0x21, 0xff}
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)

	dot := color.RGBA{0x8b, 0x93, 0xa3, 0xff}
	if running {
		dot = color.RGBA{0x2f, 0xbf, 0x71, 0xff}
	}
	cx, cy, r := float64(size)/2, float64(size)/2, 20.0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x)+0.5 - cx
			dy := float64(y)+0.5 - cy
			if math.Sqrt(dx*dx+dy*dy) <= r {
				img.Set(x, y, dot)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
