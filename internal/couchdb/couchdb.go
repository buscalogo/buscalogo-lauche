package couchdb

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"buscalogo-agent/assets"
	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/paths"
	"buscalogo-agent/internal/process"
)

const releaseName = "couchdb"

type Service struct {
	cfg  *config.Config
	buf  *logx.Buffer
	proc *process.Managed

	dbInfoMu    sync.Mutex
	dbInfoCache map[string]DbInfo
	dbInfoAt    time.Time
}

// DbInfo é o resumo de um database CouchDB (GET /{db}).
type DbInfo struct {
	Name     string `json:"name"`
	DocCount int64  `json:"doc_count"`
	FileSize int64  `json:"file_size"`
	DataSize int64  `json:"data_size"`
	Error    string `json:"error,omitempty"`
}

func New(cfg *config.Config, buf *logx.Buffer) *Service {
	return &Service{cfg: cfg, buf: buf, dbInfoCache: map[string]DbInfo{}}
}

func (s *Service) ReleaseRoot() (string, error) {
	isExec := func(p string) bool {
		info, err := os.Stat(p)
		return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
	}
	binPath := func(root string) string {
		return filepath.Join(root, "bin", "couchdb")
	}
	hasRelease := func(root string) bool {
		return isExec(binPath(root))
	}

	switch s.cfg.CouchDB.Mode {
	case "external":
		if s.cfg.CouchDB.ExternalPath != "" {
			p := s.cfg.CouchDB.ExternalPath
			if hasRelease(p) {
				return p, nil
			}
			if isExec(p) {
				return filepath.Dir(filepath.Dir(p)), nil
			}
		}
		for _, root := range []string{"/opt/couchdb", "/usr/lib/couchdb"} {
			if hasRelease(root) {
				return root, nil
			}
		}
		if isExec("/usr/bin/couchdb") {
			return "/usr", nil
		}
		return "", fmt.Errorf("modo external mas release couchdb não encontrado")
	default:
		// Instalação .deb: release completo em /opt (somente leitura; dados em ~/.buscalogo).
		if releaseLooksComplete(bundledReleasePath) {
			return bundledReleasePath, nil
		}
		if hasRelease(bundledReleasePath) {
			s.buf.Warnf("couchdb", "release em %s incompleto — tentando cópia local", bundledReleasePath)
		}

		bin, err := paths.Bin()
		if err != nil {
			return "", err
		}
		userRoot := filepath.Join(bin, releaseName)
		if releaseLooksComplete(userRoot) {
			return userRoot, nil
		}
		if isExec(filepath.Join(userRoot, "bin", "couchdb")) {
			s.buf.Warnf("couchdb", "release incompleto em %s — recopiando", userRoot)
			_ = os.RemoveAll(userRoot)
		}

		if hasRelease(bundledReleasePath) {
			root, err := ensureUserRelease(bin, bundledReleasePath)
			if err != nil {
				return "", err
			}
			s.buf.Infof("couchdb", "release copiado para %s", root)
			return root, nil
		}
		if assets.HasRelease(releaseName) {
			embedded, err := assets.EnsureRelease(releaseName, bin)
			if err != nil {
				return "", err
			}
			return embedded, nil
		}
		for _, root := range []string{"/opt/couchdb"} {
			if hasRelease(root) {
				s.buf.Infof("couchdb", "usando release do sistema em %s", root)
				return root, nil
			}
		}
		return "", fmt.Errorf("release couchdb não encontrado (embuta com 'make assets-couchdb' ou instale no sistema)")
	}
}

func (s *Service) BinaryPath() (string, error) {
	root, err := s.ReleaseRoot()
	if err != nil {
		return "", err
	}
	p := filepath.Join(root, "bin", "couchdb")
	info, err := os.Stat(p)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("binário couchdb inválido em %s", p)
	}
	return p, nil
}

func (s *Service) ConfigDir() (string, error) {
	data, err := paths.Data()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(data, "couchdb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *Service) LocalIniPath() (string, error) {
	dir, err := s.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "local.ini"), nil
}

func (s *Service) BaseURL() string {
	host := s.cfg.CouchDB.Listen
	if host == "" {
		host = "127.0.0.1"
	}
	port := s.cfg.CouchDB.Port
	if port == 0 {
		port = 5984
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

func (s *Service) WriteLocalIni() (string, error) {
	dir, err := s.ConfigDir()
	if err != nil {
		return "", err
	}
	iniPath, err := s.LocalIniPath()
	if err != nil {
		return "", err
	}

	nodeUUID, err := s.ensureNodeUUID(dir)
	if err != nil {
		return "", err
	}

	adminUser, adminPass, err := s.ensureAdminCredentials(dir)
	if err != nil {
		return "", err
	}

	dbDir := filepath.Join(dir, "databases")
	idxDir := filepath.Join(dir, "indexes")
	logDir := filepath.Join(dir, "logs")
	for _, d := range []string{dbDir, idxDir, logDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", err
		}
	}

	root, err := s.ReleaseRoot()
	if err != nil {
		return "", err
	}
	if err := s.patchRelease(root, filepath.Join(logDir, "couchdb.log")); err != nil {
		s.buf.Warnf("couchdb", "patch release: %v", err)
	}

	content := renderLocalINI(renderOpts{
		DatabaseDir: dbDir,
		IndexDir:    idxDir,
		LogFile:     filepath.Join(logDir, "couchdb.log"),
		UUID:        nodeUUID,
		Bind:        s.cfg.CouchDB.Listen,
		Port:        s.cfg.CouchDB.Port,
		AdminUser:   adminUser,
		AdminPass:   adminPass,
	})
	if err := os.WriteFile(iniPath, []byte(content), 0o600); err != nil {
		return "", err
	}
	return iniPath, nil
}

type renderOpts struct {
	DatabaseDir string
	IndexDir    string
	LogFile     string
	UUID        string
	Bind        string
	Port        int
	AdminUser   string
	AdminPass   string
}

func renderLocalINI(opts renderOpts) string {
	if opts.Bind == "" {
		opts.Bind = "127.0.0.1"
	}
	if opts.Port == 0 {
		opts.Port = 5984
	}

	var b strings.Builder
	fmt.Fprintf(&b, "; Gerado pelo BuscaLogo Agent — NAO EDITAR MANUALMENTE\n\n")
	fmt.Fprintf(&b, "[couchdb]\n")
	fmt.Fprintf(&b, "database_dir = %s\n", opts.DatabaseDir)
	fmt.Fprintf(&b, "view_index_dir = %s\n", opts.IndexDir)
	fmt.Fprintf(&b, "uuid = %s\n", opts.UUID)
	fmt.Fprintf(&b, "single_node = true\n\n")

	fmt.Fprintf(&b, "[log]\n")
	fmt.Fprintf(&b, "writer = file\n")
	fmt.Fprintf(&b, "file = %s\n", opts.LogFile)
	fmt.Fprintf(&b, "level = notice\n\n")

	fmt.Fprintf(&b, "[chttpd]\n")
	fmt.Fprintf(&b, "bind_address = %s\n", opts.Bind)
	fmt.Fprintf(&b, "port = %d\n\n", opts.Port)

	fmt.Fprintf(&b, "[chttpd_auth]\n")
	fmt.Fprintf(&b, "require_valid_user = false\n\n")

	fmt.Fprintf(&b, "[admins]\n")
	fmt.Fprintf(&b, "%s = %s\n", opts.AdminUser, opts.AdminPass)
	return b.String()
}

// ensureAdminCredentials garante usuário/senha admin exigidos pelo CouchDB 3.x.
func (s *Service) ensureAdminCredentials(dir string) (user, pass string, err error) {
	user = strings.TrimSpace(s.cfg.CouchDB.AdminUser)
	pass = s.cfg.CouchDB.AdminPassword
	if user == "" {
		user = "buscalogo"
	}
	credPath := filepath.Join(dir, "admin.password")
	if pass == "" {
		if data, readErr := os.ReadFile(credPath); readErr == nil {
			pass = strings.TrimSpace(string(data))
		}
	}
	if pass == "" {
		pass, err = randomPassword(24)
		if err != nil {
			return "", "", err
		}
		if err := os.WriteFile(credPath, []byte(pass), 0o600); err != nil {
			return "", "", err
		}
		s.cfg.CouchDB.AdminUser = user
		s.cfg.CouchDB.AdminPassword = pass
		if saveErr := s.cfg.Save(); saveErr != nil {
			s.buf.Warnf("couchdb", "salvar credencial admin na config: %v", saveErr)
		}
		s.buf.Infof("couchdb", "credencial admin gerada (user=%s)", user)
	} else if s.cfg.CouchDB.AdminPassword == "" {
		s.cfg.CouchDB.AdminUser = user
		s.cfg.CouchDB.AdminPassword = pass
	}
	return user, pass, nil
}

// RegenerateAdminPassword gera nova senha admin, persiste na config/INI e reinicia o CouchDB.
func (s *Service) RegenerateAdminPassword() (user, pass string, err error) {
	dir, err := s.ConfigDir()
	if err != nil {
		return "", "", err
	}
	user = strings.TrimSpace(s.cfg.CouchDB.AdminUser)
	if user == "" {
		user = "buscalogo"
	}
	pass, err = randomPassword(24)
	if err != nil {
		return "", "", err
	}
	credPath := filepath.Join(dir, "admin.password")
	if err := os.WriteFile(credPath, []byte(pass), 0o600); err != nil {
		return "", "", err
	}
	s.cfg.CouchDB.AdminUser = user
	s.cfg.CouchDB.AdminPassword = pass
	if err := s.cfg.Save(); err != nil {
		return "", "", err
	}
	if _, err := s.WriteLocalIni(); err != nil {
		return "", "", err
	}
	if s.proc != nil && s.Status().State == process.StateRunning {
		if err := s.Restart(); err != nil {
			return "", "", err
		}
	}
	s.buf.Infof("couchdb", "senha admin regenerada (user=%s)", user)
	return user, pass, nil
}

func randomPassword(n int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(b), nil
}

// patchRelease ajusta o release embutido: desativa log em /var/log e reforça override em local.d.
func (s *Service) patchRelease(root, logFile string) error {
	clean := filepath.Clean(root)
	if strings.HasPrefix(clean, "/opt/") {
		return nil
	}
	localD := filepath.Join(root, "etc", "local.d")
	if err := os.MkdirAll(localD, 0o755); err != nil {
		return err
	}
	filelog := filepath.Join(root, "etc", "default.d", "10-filelog.ini")
	if _, err := os.Stat(filelog); err == nil {
		if err := os.Rename(filelog, filelog+".disabled"); err != nil {
			// local.ini já define [log]; ignorar se release for somente leitura
			s.buf.Warnf("couchdb", "desativar 10-filelog.ini: %v", err)
		}
	}
	override := fmt.Sprintf("; BuscaLogo Agent\n[log]\nwriter = file\nfile = %s\nlevel = notice\n", logFile)
	return os.WriteFile(filepath.Join(localD, "99-buscalogo-log.ini"), []byte(override), 0o644)
}

func (s *Service) iniEnv(root, ini string) []string {
	defaultIni := filepath.Join(root, "etc", "default.ini")
	if abs, err := filepath.Abs(defaultIni); err == nil {
		defaultIni = abs
	}
	if abs, err := filepath.Abs(ini); err == nil {
		ini = abs
	}
	extra := fmt.Sprintf("COUCHDB_INI_FILES=%s %s", defaultIni, ini)
	return append(os.Environ(), extra)
}

func (s *Service) ensureNodeUUID(dir string) (string, error) {
	path := filepath.Join(dir, "node.uuid")
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	}
	id, err := newNodeUUID()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

func newNodeUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Service) Start() error {
	root, err := s.ReleaseRoot()
	if err != nil {
		return err
	}
	binary, err := s.BinaryPath()
	if err != nil {
		return err
	}
	ini, err := s.WriteLocalIni()
	if err != nil {
		return err
	}

	if err := process.KillExistingByBinary(s.buf, "couchdb", binary); err != nil {
		s.buf.Warnf("couchdb", "limpeza de processos antigos: %v", err)
	}

	if s.proc == nil {
		s.proc = process.New(process.Options{
			Name:        "CouchDB",
			Binary:      binary,
			Dir:         root,
			Env:         s.iniEnv(root, ini),
			LogSource:   "couchdb",
			LogBuf:      s.buf,
			AutoRestart: true,
			PreStart: func() error {
				binary, _ := s.BinaryPath()
				if err := process.KillExistingByBinary(s.buf, "couchdb", binary); err != nil {
					s.buf.Warnf("couchdb", "PreStart: limpeza de processos: %v", err)
				}
				ini, err := s.WriteLocalIni()
				if err != nil {
					return err
				}
				root, err := s.ReleaseRoot()
				if err != nil {
					return err
				}
				s.proc.SetEnv(s.iniEnv(root, ini))
				return nil
			},
		})
	} else {
		s.proc.SetEnv(s.iniEnv(root, ini))
	}
	if err := s.proc.Start(); err != nil {
		return err
	}
	go s.bootstrapDatabases()
	return nil
}

func (s *Service) bootstrapDatabases() {
	names := s.cfg.CouchDB.Databases
	if len(names) == 0 {
		names = config.DefaultCouchDBDatabases
	}
	client := s.httpClient()
	base := s.BaseURL()

	for attempt := 1; attempt <= 30; attempt++ {
		if s.Reachable(client) {
			break
		}
		time.Sleep(500 * time.Millisecond)
		if attempt == 30 {
			s.buf.Warnf("couchdb", "timeout aguardando CouchDB ficar pronto")
			return
		}
	}

	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		req, err := http.NewRequest(http.MethodPut, base+"/"+name, nil)
		if err != nil {
			s.buf.Warnf("couchdb", "criar db %s: %v", name, err)
			continue
		}
		s.authRequest(req)
		resp, err := client.Do(req)
		if err != nil {
			s.buf.Warnf("couchdb", "criar db %s: %v", name, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusCreated, http.StatusPreconditionFailed:
			s.buf.Infof("couchdb", "database %s pronto", name)
		default:
			s.buf.Warnf("couchdb", "criar db %s: HTTP %d %s", name, resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
}

func (s *Service) Stop() error {
	if s.proc == nil {
		return nil
	}
	return s.proc.Stop()
}

func (s *Service) Restart() error {
	if _, err := s.WriteLocalIni(); err != nil {
		return err
	}
	if s.proc == nil {
		return s.Start()
	}
	return s.proc.Restart()
}

func (s *Service) Status() process.Status {
	if s.proc == nil {
		return process.Status{Name: "CouchDB", State: process.StateStopped}
	}
	return s.proc.Status()
}

func (s *Service) Managed() *process.Managed { return s.proc }

func (s *Service) httpClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Second}
}

func (s *Service) httpClientLong() *http.Client {
	return &http.Client{Timeout: 90 * time.Second}
}

// httpClientList: listas com include_docs — timeout curto para não travar o painel.
func (s *Service) httpClientList() *http.Client {
	return &http.Client{Timeout: 12 * time.Second}
}

func (s *Service) authRequest(req *http.Request) {
	user := s.cfg.CouchDB.AdminUser
	pass := s.cfg.CouchDB.AdminPassword
	if user != "" && pass != "" {
		req.SetBasicAuth(user, pass)
	}
}

func (s *Service) Reachable(client *http.Client) bool {
	if client == nil {
		client = s.httpClient()
	}
	req, err := http.NewRequest(http.MethodGet, s.BaseURL()+"/_up", nil)
	if err != nil {
		return false
	}
	s.authRequest(req)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Info retorna status HTTP do CouchDB e lista de databases.
func (s *Service) Info() map[string]any {
	info := map[string]any{
		"url":             s.BaseURL(),
		"reachable":       false,
		"admin_user":      s.cfg.CouchDB.AdminUser,
		"admin_password":  s.cfg.CouchDB.AdminPassword,
		"credential_file": s.credentialFilePath(),
		"config_file":     s.cfg.Path(),
	}
	client := s.httpClient()
	if !s.Reachable(client) {
		return info
	}
	info["reachable"] = true

	req, err := http.NewRequest(http.MethodGet, s.BaseURL()+"/", nil)
	if err == nil {
		s.authRequest(req)
		if resp, err := client.Do(req); err == nil {
			defer resp.Body.Close()
			var root map[string]any
			if json.NewDecoder(resp.Body).Decode(&root) == nil {
				if v, ok := root["version"].(string); ok {
					info["version"] = v
				}
			}
		}
	}

	dbs, err := s.ListDatabases()
	if err != nil {
		info["databases"] = []string{}
	} else {
		info["databases"] = dbs
	}

	// Só detalha DBs padrão (leve). Host DBs: contar nomes — GET em cada um
	// travava o painel (legado 5.7GB + N hosts a cada poll).
	detailNames := make([]string, 0, len(config.DefaultCouchDBDatabases))
	seen := map[string]bool{}
	for _, name := range config.DefaultCouchDBDatabases {
		if !seen[name] {
			seen[name] = true
			detailNames = append(detailNames, name)
		}
	}
	details := s.DbInfoMany(detailNames)
	info["database_details"] = details

	hostDBs := 0
	for _, name := range dbs {
		if strings.HasPrefix(name, "bl_scraping_") {
			hostDBs++
		}
	}
	var legacyDocs, legacyBytes int64
	for _, d := range details {
		if d.Name == "buscalogo_scraping" {
			legacyDocs = d.DocCount
			legacyBytes = d.FileSize
			break
		}
	}
	info["scraping"] = map[string]any{
		"databases":      hostDBs + boolToInt(legacyDocs > 0),
		"host_databases": hostDBs,
		"doc_count":      legacyDocs, // legado; hosts na aba Scraper
		"file_size":      legacyBytes,
		"legacy_docs":    legacyDocs,
		"legacy_bytes":   legacyBytes,
		"legacy_db":      "buscalogo_scraping",
		"host_prefix":    "bl_scraping_",
	}
	return info
}

func boolToInt(ok bool) int {
	if ok {
		return 1
	}
	return 0
}

// ListDatabases retorna todos os nomes de DB.
func (s *Service) ListDatabases() ([]string, error) {
	client := s.httpClient()
	req, err := http.NewRequest(http.MethodGet, s.BaseURL()+"/_all_dbs", nil)
	if err != nil {
		return nil, err
	}
	s.authRequest(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET _all_dbs: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var dbs []string
	if err := json.NewDecoder(resp.Body).Decode(&dbs); err != nil {
		return nil, err
	}
	return dbs, nil
}

// InvalidateDbInfoCache limpa o cache de tamanhos (após delete em massa).
func (s *Service) InvalidateDbInfoCache() {
	s.dbInfoMu.Lock()
	defer s.dbInfoMu.Unlock()
	s.dbInfoCache = map[string]DbInfo{}
	s.dbInfoAt = time.Time{}
}

func (s *Service) invalidateOneDbInfo(name string) {
	s.dbInfoMu.Lock()
	defer s.dbInfoMu.Unlock()
	delete(s.dbInfoCache, name)
}

// DbInfo consulta GET /{db} (com cache curto).
func (s *Service) DbInfo(name string) (DbInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return DbInfo{}, fmt.Errorf("nome de database vazio")
	}
	s.dbInfoMu.Lock()
	if time.Since(s.dbInfoAt) < 60*time.Second {
		if cached, ok := s.dbInfoCache[name]; ok {
			s.dbInfoMu.Unlock()
			return cached, nil
		}
	} else {
		// janela expirou — limpa e recomeça
		s.dbInfoCache = map[string]DbInfo{}
		s.dbInfoAt = time.Now()
	}
	s.dbInfoMu.Unlock()

	info, err := s.fetchDbInfo(name)
	if err != nil {
		return info, err
	}
	s.dbInfoMu.Lock()
	if s.dbInfoCache == nil {
		s.dbInfoCache = map[string]DbInfo{}
	}
	if s.dbInfoAt.IsZero() {
		s.dbInfoAt = time.Now()
	}
	s.dbInfoCache[name] = info
	s.dbInfoMu.Unlock()
	return info, nil
}

// DbInfoMany consulta vários DBs; falhas parciais preenchem Error sem abortar.
func (s *Service) DbInfoMany(names []string) []DbInfo {
	out := make([]DbInfo, 0, len(names))
	for _, name := range names {
		info, err := s.DbInfo(name)
		if err != nil {
			out = append(out, DbInfo{Name: name, Error: err.Error()})
			continue
		}
		out = append(out, info)
	}
	return out
}

func (s *Service) fetchDbInfo(name string) (DbInfo, error) {
	client := s.httpClient()
	u := s.BaseURL() + "/" + url.PathEscape(name)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return DbInfo{Name: name}, err
	}
	s.authRequest(req)
	resp, err := client.Do(req)
	if err != nil {
		return DbInfo{Name: name}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return DbInfo{Name: name}, fmt.Errorf("not found")
	}
	if resp.StatusCode >= 300 {
		return DbInfo{Name: name}, fmt.Errorf("GET %s: HTTP %d %s", u, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var raw struct {
		DBName   string `json:"db_name"`
		DocCount int64  `json:"doc_count"`
		DiskSize int64  `json:"disk_size"`
		DataSize int64  `json:"data_size"`
		Sizes    *struct {
			File   int64 `json:"file"`
			Active int64 `json:"active"`
			External int64 `json:"external"`
		} `json:"sizes"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return DbInfo{Name: name}, err
	}
	info := DbInfo{
		Name:     name,
		DocCount: raw.DocCount,
		FileSize: raw.DiskSize,
		DataSize: raw.DataSize,
	}
	if raw.DBName != "" {
		info.Name = raw.DBName
	}
	if raw.Sizes != nil {
		if raw.Sizes.File > 0 {
			info.FileSize = raw.Sizes.File
		}
		if raw.Sizes.Active > 0 {
			info.DataSize = raw.Sizes.Active
		}
	}
	return info, nil
}

// EnsureDB cria o database se não existir (PUT).
func (s *Service) EnsureDB(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("nome de database vazio")
	}
	client := s.httpClient()
	req, err := http.NewRequest(http.MethodPut, s.BaseURL()+"/"+name, nil)
	if err != nil {
		return err
	}
	s.authRequest(req)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusPreconditionFailed, http.StatusOK:
		s.invalidateOneDbInfo(name)
		return nil
	default:
		return fmt.Errorf("PUT %s: HTTP %d %s", name, resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

// DeleteDB remove um database inteiro.
func (s *Service) DeleteDB(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("nome de database vazio")
	}
	client := s.httpClient()
	req, err := http.NewRequest(http.MethodDelete, s.BaseURL()+"/"+name, nil)
	if err != nil {
		return err
	}
	s.authRequest(req)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		s.invalidateOneDbInfo(name)
		return nil
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("DELETE %s: HTTP %d %s", name, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	s.invalidateOneDbInfo(name)
	return nil
}

func (s *Service) credentialFilePath() string {
	dir, err := s.ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "admin.password")
}

// ConnectionURL retorna a URL HTTP do CouchDB com credenciais (para PouchDB remoto).
func (s *Service) ConnectionURL() (string, error) {
	user := strings.TrimSpace(s.cfg.CouchDB.AdminUser)
	pass := s.cfg.CouchDB.AdminPassword
	host := s.cfg.CouchDB.Listen
	if host == "" {
		host = "127.0.0.1"
	}
	port := s.cfg.CouchDB.Port
	if port == 0 {
		port = 5984
	}
	if user != "" && pass != "" {
		return fmt.Sprintf("http://%s:%s@%s:%d", user, pass, host, port), nil
	}
	return fmt.Sprintf("http://%s:%d", host, port), nil
}

// Ping verifica conectividade (útil para testes).
func (s *Service) Ping() error {
	client := s.httpClient()
	if !s.Reachable(client) {
		return fmt.Errorf("couchdb não responde em %s", s.BaseURL())
	}
	return nil
}

// DocRow é um documento retornado por ListDocs.
type DocRow struct {
	ID  string          `json:"id"`
	Rev string          `json:"rev"`
	Doc json.RawMessage `json:"doc"`
}

// GetDoc lê um documento pelo ID (_rev vazio se não existir).
func (s *Service) GetDoc(db, docID string) ([]byte, string, error) {
	client := s.httpClient()
	url := s.BaseURL() + "/" + db + "/" + docID
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	s.authRequest(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", fmt.Errorf("not found")
	}
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("GET %s: HTTP %d %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, "", nil
	}
	rev := ""
	if r, ok := doc["_rev"]; ok {
		_ = json.Unmarshal(r, &rev)
		delete(doc, "_rev")
	}
	delete(doc, "_id")
	out, err := json.Marshal(doc)
	return out, rev, err
}

// ListDocs lista documentos por intervalo de _id (startkey/endkey).
func (s *Service) ListDocs(db, startKey, endKey string, limit int) ([]DocRow, error) {
	return s.ListDocsPaged(db, startKey, endKey, limit, 0)
}

// ListDocsPaged lista documentos com skip (paginação para varreduras completas).
func (s *Service) ListDocsPaged(db, startKey, endKey string, limit, skip int) ([]DocRow, error) {
	if limit <= 0 {
		limit = 50
	}
	if skip < 0 {
		skip = 0
	}
	client := s.httpClientList()
	u := fmt.Sprintf("%s/%s/_all_docs?include_docs=true&limit=%d&skip=%d", s.BaseURL(), db, limit, skip)
	if startKey != "" {
		u += "&startkey=" + jsonKey(startKey) + "&endkey=" + jsonKey(endKey)
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	s.authRequest(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: HTTP %d %s", u, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		Rows []struct {
			ID    string          `json:"id"`
			Key   string          `json:"key"`
			Value struct {
				Rev string `json:"rev"`
			} `json:"value"`
			Doc json.RawMessage `json:"doc"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	out := make([]DocRow, 0, len(parsed.Rows))
	for _, row := range parsed.Rows {
		if strings.HasPrefix(row.ID, "_") {
			continue
		}
		out = append(out, DocRow{ID: row.ID, Rev: row.Value.Rev, Doc: row.Doc})
	}
	return out, nil
}

func jsonKey(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// DeleteDoc remove um documento pelo ID e rev.
func (s *Service) DeleteDoc(db, docID, rev string) error {
	client := s.httpClient()
	url := s.BaseURL() + "/" + db + "/" + docID
	if rev != "" {
		url += "?rev=" + rev
	}
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	s.authRequest(req)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("DELETE %s: HTTP %d %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// PutDoc grava um documento JSON em um database (helper para integração futura).
func (s *Service) PutDoc(db, docID string, body []byte) error {
	client := s.httpClient()
	if strings.HasPrefix(docID, "_design/") {
		client = s.httpClientLong()
	}
	url := s.BaseURL() + "/" + db + "/" + docID
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	s.authRequest(req)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PUT %s: HTTP %d %s", url, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

// GetDocLong é GetDoc com timeout maior (design docs / ops lentas).
func (s *Service) GetDocLong(db, docID string) ([]byte, string, error) {
	client := s.httpClientLong()
	url := s.BaseURL() + "/" + db + "/" + docID
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	s.authRequest(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", fmt.Errorf("not found")
	}
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("GET %s: HTTP %d %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, "", nil
	}
	rev := ""
	if r, ok := doc["_rev"]; ok {
		_ = json.Unmarshal(r, &rev)
		delete(doc, "_rev")
	}
	delete(doc, "_id")
	out, err := json.Marshal(doc)
	return out, rev, err
}

// ViewRow é uma linha de uma view CouchDB.
type ViewRow struct {
	ID    string          `json:"id"`
	Key   json.RawMessage `json:"key"`
	Value json.RawMessage `json:"value"`
}

// EnsureDesignDoc cria/atualiza um design doc se a rev atual for diferente.
func (s *Service) EnsureDesignDoc(db, designID string, body map[string]any) error {
	if !strings.HasPrefix(designID, "_design/") {
		designID = "_design/" + designID
	}
	existing, rev, err := s.GetDocLong(db, designID)
	if err == nil && len(existing) > 0 {
		var prev map[string]json.RawMessage
		_ = json.Unmarshal(existing, &prev)
		if v, ok := body["views"]; ok {
			want, _ := json.Marshal(v)
			if cur, ok := prev["views"]; ok && bytes.Equal(cur, want) {
				return nil
			}
		}
		body["_rev"] = rev
	} else if err != nil && !strings.Contains(err.Error(), "not found") {
		return err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return s.PutDoc(db, designID, raw)
}

// QueryView consulta uma view. group/reduce controlam agregação.
func (s *Service) QueryView(db, design, view string, opts map[string]string) ([]ViewRow, error) {
	client := s.httpClientLong()
	u := fmt.Sprintf("%s/%s/_design/%s/_view/%s", s.BaseURL(), db, design, view)
	q := url.Values{}
	for k, v := range opts {
		q.Set(k, v)
	}
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	s.authRequest(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: HTTP %d %s", u, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		Rows []ViewRow `json:"rows"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	return parsed.Rows, nil
}
