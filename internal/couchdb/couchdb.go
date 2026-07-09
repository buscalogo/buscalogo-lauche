package couchdb

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
}

func New(cfg *config.Config, buf *logx.Buffer) *Service {
	return &Service{cfg: cfg, buf: buf}
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
		bin, err := paths.Bin()
		if err != nil {
			return "", err
		}
		userRoot := filepath.Join(bin, releaseName)
		if hasRelease(userRoot) {
			if hasRelease(bundledReleasePath) {
				if releaseManifestID(userRoot) != releaseManifestID(bundledReleasePath) {
					s.buf.Infof("couchdb", "atualizando release em %s", userRoot)
				} else {
					return userRoot, nil
				}
			} else {
				return userRoot, nil
			}
		}
		source := ""
		if hasRelease(bundledReleasePath) {
			source = bundledReleasePath
		} else if assets.HasRelease(releaseName) {
			embedded, err := assets.EnsureRelease(releaseName, bin)
			if err != nil {
				return "", err
			}
			return embedded, nil
		}
		if source != "" {
			root, err := ensureUserRelease(bin, source)
			if err != nil {
				return "", err
			}
			s.buf.Infof("couchdb", "release copiado para %s (gravável pelo usuário)", root)
			return root, nil
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
		"url":            s.BaseURL(),
		"reachable":      false,
		"admin_user":     s.cfg.CouchDB.AdminUser,
		"admin_password": s.cfg.CouchDB.AdminPassword,
		"credential_file": s.credentialFilePath(),
		"config_file":    s.cfg.Path(),
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

	req, err = http.NewRequest(http.MethodGet, s.BaseURL()+"/_all_dbs", nil)
	if err == nil {
		s.authRequest(req)
		if resp, err := client.Do(req); err == nil {
			defer resp.Body.Close()
			var dbs []string
			if json.NewDecoder(resp.Body).Decode(&dbs) == nil {
				info["databases"] = dbs
			}
		}
	}
	if _, ok := info["databases"]; !ok {
		info["databases"] = []string{}
	}
	return info
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
	if limit <= 0 {
		limit = 50
	}
	client := s.httpClient()
	u := fmt.Sprintf("%s/%s/_all_docs?include_docs=true&limit=%d", s.BaseURL(), db, limit)
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
