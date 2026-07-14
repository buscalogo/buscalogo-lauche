package account

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"buscalogo-agent/internal/couchdb"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/paths"
)

const (
	usersDB    = "buscalogo_main"
	configDB   = "buscalogo_config"
	serverDoc  = "server_config"
	exportVer  = "2.0"
	exportVer1 = "1.0"
	bcryptCost = 10
	cookieName = "bl_account_session"
)

// CreatedBy é a autoria embutida nos scrapes (compatível com bl-scraper-server + chave).
type CreatedBy struct {
	ServerID    string `json:"serverId"`
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	PublicKey   string `json:"publicKey,omitempty"`
}

// PublicUser é o perfil sem segredos.
type PublicUser struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	PublicKey   string `json:"publicKey"`
	CreatedAt   string `json:"createdAt"`
}

type userDoc struct {
	ID           string `json:"_id"`
	Rev          string `json:"_rev,omitempty"`
	DocType      string `json:"doc_type"`
	UserID       string `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	PasswordHash string `json:"password_hash"`
	PublicKey    string `json:"public_key"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type serverConfigDoc struct {
	ID       string `json:"_id"`
	Rev      string `json:"_rev,omitempty"`
	DocType  string `json:"doc_type"`
	ServerID string `json:"server_id"`
}

// Backup é o JSON de export (v2 inclui privateKey).
type Backup struct {
	ExportVersion string `json:"exportVersion"`
	ExportedAt    string `json:"exportedAt"`
	Source        string `json:"source"`
	ServerID      string `json:"serverId"`
	User          struct {
		ID          string `json:"id"`
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
		PublicKey   string `json:"publicKey,omitempty"`
		CreatedAt   string `json:"createdAt"`
	} `json:"user"`
	PrivateKey string `json:"privateKey,omitempty"`
}

// Service gerencia conta local, chave ed25519 e sessão.
type Service struct {
	cdb *couchdb.Service
	buf *logx.Buffer

	mu         sync.Mutex
	serverID   string
	sessionTok string
	sessionUID string
	privKey    ed25519.PrivateKey
	pubKeyHex  string
	user       *PublicUser
}

func New(cdb *couchdb.Service, buf *logx.Buffer) *Service {
	return &Service{cdb: cdb, buf: buf}
}

// useCouch: persiste em CouchDB quando habilitado; senão usa ficheiros em identity/.
func (s *Service) useCouch() bool {
	return s != nil && s.cdb != nil && s.cdb.Enabled()
}

func (s *Service) available() error {
	if s == nil {
		return fmt.Errorf("serviço de conta indisponível")
	}
	return nil
}

// StorageBackend descreve onde a conta está guardada.
func (s *Service) StorageBackend() string {
	if s.useCouch() {
		return "couchdb"
	}
	return "file"
}

func keyPath() (string, error) {
	data, err := paths.Data()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(data, "identity")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "account.key"), nil
}

func sessionPath() (string, error) {
	data, err := paths.Data()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(data, "identity")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "session.json"), nil
}

type sessionFile struct {
	Token  string `json:"token"`
	UserID string `json:"userId"`
}

func (s *Service) EnsureServerID() (string, error) {
	if err := s.available(); err != nil {
		return "", err
	}
	s.mu.Lock()
	if s.serverID != "" {
		id := s.serverID
		s.mu.Unlock()
		return id, nil
	}
	s.mu.Unlock()

	if !s.useCouch() {
		if id, err := loadServerIDFile(); err == nil && id != "" {
			s.mu.Lock()
			s.serverID = id
			s.mu.Unlock()
			return id, nil
		}
		id := newUUID()
		if err := saveServerIDFile(id); err != nil {
			return "", err
		}
		s.mu.Lock()
		s.serverID = id
		s.mu.Unlock()
		if s.buf != nil {
			s.buf.Infof("account", "server_id gerado (ficheiro): %s", id)
		}
		return id, nil
	}

	_ = s.cdb.EnsureDB(configDB)
	body, rev, err := s.cdb.GetDoc(configDB, serverDoc)
	if err == nil && len(body) > 0 {
		var doc serverConfigDoc
		if json.Unmarshal(body, &doc) == nil && doc.ServerID != "" {
			s.mu.Lock()
			s.serverID = doc.ServerID
			s.mu.Unlock()
			_ = saveServerIDFile(doc.ServerID) // espelho local
			return doc.ServerID, nil
		}
		_ = rev
	}

	id := newUUID()
	doc := serverConfigDoc{
		ID:       serverDoc,
		DocType:  "server_config",
		ServerID: id,
	}
	if rev != "" {
		doc.Rev = rev
	}
	raw, _ := json.Marshal(doc)
	if err := s.cdb.PutDoc(configDB, serverDoc, raw); err != nil {
		// race: relê
		if body2, _, err2 := s.cdb.GetDoc(configDB, serverDoc); err2 == nil {
			var doc2 serverConfigDoc
			if json.Unmarshal(body2, &doc2) == nil && doc2.ServerID != "" {
				s.mu.Lock()
				s.serverID = doc2.ServerID
				s.mu.Unlock()
				_ = saveServerIDFile(doc2.ServerID)
				return doc2.ServerID, nil
			}
		}
		// CouchDB caiu: fallback ficheiro
		if sid, err2 := loadServerIDFile(); err2 == nil && sid != "" {
			s.mu.Lock()
			s.serverID = sid
			s.mu.Unlock()
			return sid, nil
		}
		if err2 := saveServerIDFile(id); err2 == nil {
			s.mu.Lock()
			s.serverID = id
			s.mu.Unlock()
			if s.buf != nil {
				s.buf.Warnf("account", "server_id em ficheiro (CouchDB falhou: %v)", err)
			}
			return id, nil
		}
		return "", err
	}
	s.mu.Lock()
	s.serverID = id
	s.mu.Unlock()
	_ = saveServerIDFile(id)
	if s.buf != nil {
		s.buf.Infof("account", "server_id gerado: %s", id)
	}
	return id, nil
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func generateKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

func writePrivateKey(priv ed25519.PrivateKey) error {
	path, err := keyPath()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(hex.EncodeToString(priv)), 0o600)
}

func readPrivateKey() (ed25519.PrivateKey, error) {
	path, err := keyPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("chave privada inválida")
	}
	return ed25519.PrivateKey(raw), nil
}

func (s *Service) findByUsername(username string) (*userDoc, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !s.useCouch() {
		return findUserFileByUsername(username)
	}
	rows, err := s.cdb.ListDocsPaged(usersDB, "user_", "user_\ufff0", 500, 0)
	if err != nil {
		// fallback ficheiro se CouchDB não responder
		if doc, err2 := findUserFileByUsername(username); err2 == nil && doc != nil {
			return doc, nil
		}
		return nil, err
	}
	for _, row := range rows {
		var doc userDoc
		if json.Unmarshal(row.Doc, &doc) != nil {
			continue
		}
		if doc.DocType != "user" {
			continue
		}
		if strings.EqualFold(doc.Username, username) {
			doc.ID = row.ID
			doc.Rev = row.Rev
			return &doc, nil
		}
	}
	return nil, nil
}

func (s *Service) findByID(userID string) (*userDoc, error) {
	if !s.useCouch() {
		return loadUserFile(userID)
	}
	body, rev, err := s.cdb.GetDoc(usersDB, "user_"+userID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, nil
		}
		if doc, err2 := loadUserFile(userID); err2 == nil && doc != nil {
			return doc, nil
		}
		return nil, err
	}
	var doc userDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	if doc.DocType != "user" {
		return nil, nil
	}
	doc.ID = "user_" + userID
	doc.Rev = rev
	if doc.UserID == "" {
		doc.UserID = userID
	}
	return &doc, nil
}

func (s *Service) putUser(doc *userDoc) error {
	if doc == nil {
		return fmt.Errorf("userDoc nulo")
	}
	if !s.useCouch() {
		return saveUserFile(doc)
	}
	_ = s.cdb.EnsureDB(usersDB)
	raw, _ := json.Marshal(doc)
	if err := s.cdb.PutDoc(usersDB, doc.ID, raw); err != nil {
		if err2 := saveUserFile(doc); err2 == nil {
			if s.buf != nil {
				s.buf.Warnf("account", "utilizador gravado em ficheiro (CouchDB: %v)", err)
			}
			return nil
		}
		return err
	}
	_ = saveUserFile(doc) // espelho
	return nil
}

func publicFromDoc(doc *userDoc) *PublicUser {
	return &PublicUser{
		ID:          doc.UserID,
		Username:    doc.Username,
		DisplayName: doc.DisplayName,
		PublicKey:   doc.PublicKey,
		CreatedAt:   doc.CreatedAt,
	}
}

func newSessionToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Service) setSession(user *PublicUser, priv ed25519.PrivateKey) (token string, err error) {
	token, err = newSessionToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.sessionTok = token
	s.sessionUID = user.ID
	s.user = user
	s.privKey = priv
	s.pubKeyHex = user.PublicKey
	s.mu.Unlock()
	_ = s.writeSessionFile(token, user.ID)
	return token, nil
}

func (s *Service) writeSessionFile(token, userID string) error {
	path, err := sessionPath()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(sessionFile{Token: token, UserID: userID})
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func (s *Service) clearSessionFile() {
	path, err := sessionPath()
	if err != nil {
		return
	}
	_ = os.Remove(path)
}

func (s *Service) clearSession() {
	s.mu.Lock()
	s.sessionTok = ""
	s.sessionUID = ""
	s.user = nil
	s.privKey = nil
	s.pubKeyHex = ""
	s.mu.Unlock()
	s.clearSessionFile()
}

// CookieName retorna o nome do cookie de sessão.
func CookieName() string { return cookieName }

// SessionToken devolve o token atual (para renovar cookie após restore).
func (s *Service) SessionToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionTok
}

// RestoreSession recarrega sessão persistida (sobrevive a reinício do Agent).
func (s *Service) RestoreSession() {
	s.mu.Lock()
	if s.user != nil && len(s.privKey) == ed25519.PrivateKeySize {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	path, err := sessionPath()
	if err != nil {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return
	}
	var sf sessionFile
	if json.Unmarshal(raw, &sf) != nil || sf.Token == "" || sf.UserID == "" {
		if s.buf != nil {
			s.buf.Warnf("account", "session.json inválido — ignorando")
		}
		return
	}
	doc, err := s.findByID(sf.UserID)
	if err != nil {
		// store ainda a subir / falha transitória: NÃO apagar a sessão.
		if s.buf != nil {
			s.buf.Warnf("account", "restore sessão adiado (%s): %v", s.StorageBackend(), err)
		}
		return
	}
	if doc == nil {
		// Usuário realmente sumiu do banco — aí sim limpa.
		if s.buf != nil {
			s.buf.Warnf("account", "usuário da sessão não existe mais — limpando session.json")
		}
		s.clearSessionFile()
		return
	}
	priv, err := readPrivateKey()
	if err != nil {
		if s.buf != nil {
			s.buf.Warnf("account", "restore sessão: account.key indisponível: %v", err)
		}
		return
	}
	want := hex.EncodeToString(priv.Public().(ed25519.PublicKey))
	if doc.PublicKey != "" && doc.PublicKey != want {
		if s.buf != nil {
			s.buf.Warnf("account", "restore sessão: chave local não bate com a conta (use import do backup)")
		}
		return
	}
	if doc.PublicKey == "" {
		doc.PublicKey = want
	}
	user := publicFromDoc(doc)
	s.mu.Lock()
	s.sessionTok = sf.Token
	s.sessionUID = user.ID
	s.user = user
	s.privKey = priv
	s.pubKeyHex = user.PublicKey
	s.mu.Unlock()
	if s.buf != nil {
		s.buf.Infof("account", "sessão restaurada: %s", user.Username)
	}
}

// RestoreSessionRetry tenta restaurar a sessão com backoff (startup).
func (s *Service) RestoreSessionRetry(attempts int, delay time.Duration) {
	if attempts < 1 {
		attempts = 1
	}
	if delay <= 0 {
		delay = time.Second
	}
	for i := 0; i < attempts; i++ {
		s.RestoreSession()
		if s.Current() != nil {
			return
		}
		path, err := sessionPath()
		if err != nil {
			return
		}
		if _, err := os.Stat(path); err != nil {
			return // sem arquivo — nada a restaurar
		}
		time.Sleep(delay)
	}
}

// ValidateSession valida o token e devolve o usuário ativo.
func (s *Service) ValidateSession(token string) *PublicUser {
	s.RestoreSession()
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == "" || token != s.sessionTok || s.user == nil {
		return nil
	}
	u := *s.user
	return &u
}

// Current retorna o usuário da sessão ativa (sem validar cookie — uso interno).
func (s *Service) Current() *PublicUser {
	s.RestoreSession()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.user == nil {
		return nil
	}
	u := *s.user
	return &u
}

// SignScrape assina um scrape se houver sessão com chave privada.
func (s *Service) SignScrape(docID, rawURL, updatedAt string) (createdBy any, signature, signedAt string, ok bool) {
	serverID, _ := s.EnsureServerID()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.user == nil || len(s.privKey) != ed25519.PrivateKeySize {
		return nil, "", "", false
	}
	if serverID == "" {
		serverID = s.serverID
	}
	payload := rawURL + "\n" + docID + "\n" + updatedAt
	sig := ed25519.Sign(s.privKey, []byte(payload))
	cb := &CreatedBy{
		ServerID:    serverID,
		UserID:      s.user.ID,
		DisplayName: s.user.DisplayName,
		PublicKey:   s.user.PublicKey,
	}
	return cb, hex.EncodeToString(sig), updatedAt, true
}

// Register cria conta, gera keypair e inicia sessão.
func (s *Service) Register(username, password string) (*PublicUser, string, error) {
	if err := s.available(); err != nil {
		return nil, "", err
	}
	username = strings.ToLower(strings.TrimSpace(username))
	if len(username) < 2 {
		return nil, "", fmt.Errorf("username deve ter pelo menos 2 caracteres")
	}
	if len(password) < 6 {
		return nil, "", fmt.Errorf("senha deve ter pelo menos 6 caracteres")
	}
	if _, err := s.EnsureServerID(); err != nil {
		return nil, "", err
	}

	if existing, err := s.findByUsername(username); err != nil {
		return nil, "", err
	} else if existing != nil {
		return nil, "", fmt.Errorf("username já está em uso")
	}

	pub, priv, err := generateKeyPair()
	if err != nil {
		return nil, "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return nil, "", err
	}
	if err := writePrivateKey(priv); err != nil {
		return nil, "", fmt.Errorf("gravar chave privada: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	id := newUUID()
	doc := userDoc{
		ID:           "user_" + id,
		DocType:      "user",
		UserID:       id,
		Username:     username,
		DisplayName:  username,
		PasswordHash: string(hash),
		PublicKey:    hex.EncodeToString(pub),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.putUser(&doc); err != nil {
		return nil, "", err
	}
	user := publicFromDoc(&doc)
	token, err := s.setSession(user, priv)
	if err != nil {
		return nil, "", err
	}
	if s.buf != nil {
		s.buf.Infof("account", "conta criada (%s): %s", s.StorageBackend(), username)
	}
	return user, token, nil
}

// Login autentica e carrega a chave privada local.
func (s *Service) Login(username, password string) (*PublicUser, string, error) {
	if err := s.available(); err != nil {
		return nil, "", err
	}
	username = strings.ToLower(strings.TrimSpace(username))
	doc, err := s.findByUsername(username)
	if err != nil {
		return nil, "", err
	}
	if doc == nil {
		return nil, "", fmt.Errorf("usuário ou senha inválidos")
	}
	if bcrypt.CompareHashAndPassword([]byte(doc.PasswordHash), []byte(password)) != nil {
		return nil, "", fmt.Errorf("usuário ou senha inválidos")
	}
	if _, err := s.EnsureServerID(); err != nil {
		return nil, "", err
	}

	priv, err := readPrivateKey()
	if err != nil {
		// regenera se arquivo sumiu (export necessário para recuperar a antiga)
		pub, newPriv, genErr := generateKeyPair()
		if genErr != nil {
			return nil, "", fmt.Errorf("chave privada ausente: %w", err)
		}
		if wErr := writePrivateKey(newPriv); wErr != nil {
			return nil, "", wErr
		}
		doc.PublicKey = hex.EncodeToString(pub)
		doc.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		_ = s.putUser(doc)
		priv = newPriv
		if s.buf != nil {
			s.buf.Warnf("account", "chave privada regenerada para %s — scrapes antigos não verificam com a nova chave", username)
		}
	} else {
		// confere se pubkey bate
		want := hex.EncodeToString(priv.Public().(ed25519.PublicKey))
		if doc.PublicKey != "" && doc.PublicKey != want {
			return nil, "", fmt.Errorf("chave privada local não corresponde à conta — use import do backup")
		}
		if doc.PublicKey == "" {
			doc.PublicKey = want
			doc.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			_ = s.putUser(doc)
		}
	}

	user := publicFromDoc(doc)
	token, err := s.setSession(user, priv)
	if err != nil {
		return nil, "", err
	}
	return user, token, nil
}

// Logout encerra a sessão (mantém chave no disco).
func (s *Service) Logout() {
	s.clearSession()
}

// HasAccount indica se já existe pelo menos um usuário cadastrado.
func (s *Service) HasAccount() bool {
	if s == nil {
		return false
	}
	if hasUserFile() {
		return true
	}
	if !s.useCouch() {
		return false
	}
	_ = s.cdb.EnsureDB(usersDB)
	rows, err := s.cdb.ListDocsPaged(usersDB, "user_", "user_\ufff0", 20, 0)
	if err != nil {
		return false
	}
	for _, row := range rows {
		var doc userDoc
		if json.Unmarshal(row.Doc, &doc) != nil {
			continue
		}
		if doc.DocType == "user" && doc.UserID != "" {
			return true
		}
	}
	return false
}

// Me devolve status da conta + serverId.
func (s *Service) Me() map[string]any {
	s.RestoreSession()
	serverID, _ := s.EnsureServerID()
	has := s.HasAccount()
	loggedIn := s.Current() != nil
	out := map[string]any{
		"server_id":      serverID,
		"has_account":    has,
		"logged_in":      loggedIn,
		"setup_required": !has,
		"login_required": has && !loggedIn,
		"locked":         !loggedIn,
		"storage":        s.StorageBackend(),
	}
	if u := s.Current(); u != nil {
		out["user"] = u
		out["signing"] = true
	}
	return out
}

// SigningKey devolve a chave privada Ed25519 da sessão (account.key).
func (s *Service) SigningKey() (ed25519.PrivateKey, error) {
	s.RestoreSession()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.user == nil || len(s.privKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("faça login para assinar domínios")
	}
	out := make(ed25519.PrivateKey, len(s.privKey))
	copy(out, s.privKey)
	return out, nil
}

// ExportBackup gera JSON com chave privada (requer sessão).
func (s *Service) ExportBackup() (*Backup, error) {
	u := s.Current()
	if u == nil {
		return nil, fmt.Errorf("faça login para exportar")
	}
	s.mu.Lock()
	priv := s.privKey
	serverID := s.serverID
	s.mu.Unlock()
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("chave privada indisponível")
	}
	if serverID == "" {
		var err error
		serverID, err = s.EnsureServerID()
		if err != nil {
			return nil, err
		}
	}
	b := &Backup{
		ExportVersion: exportVer,
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		Source:        "buscalogo-agent",
		ServerID:      serverID,
		PrivateKey:    hex.EncodeToString(priv),
	}
	b.User.ID = u.ID
	b.User.Username = u.Username
	b.User.DisplayName = u.DisplayName
	b.User.PublicKey = u.PublicKey
	b.User.CreatedAt = u.CreatedAt
	return b, nil
}

// ImportBackup recupera conta (v1 ou v2) e define nova senha.
func (s *Service) ImportBackup(raw json.RawMessage, newPassword string) (*PublicUser, string, error) {
	if err := s.available(); err != nil {
		return nil, "", err
	}
	if len(newPassword) < 6 {
		return nil, "", fmt.Errorf("senha deve ter pelo menos 6 caracteres")
	}
	var backup Backup
	if err := json.Unmarshal(raw, &backup); err != nil {
		return nil, "", fmt.Errorf("arquivo de backup inválido")
	}
	if backup.User.ID == "" || backup.User.Username == "" {
		return nil, "", fmt.Errorf("formato de backup inválido: faltam dados do usuário")
	}
	if _, err := s.EnsureServerID(); err != nil {
		return nil, "", err
	}

	var priv ed25519.PrivateKey
	var pubHex string
	if backup.PrivateKey != "" {
		rawKey, err := hex.DecodeString(strings.TrimSpace(backup.PrivateKey))
		if err != nil || len(rawKey) != ed25519.PrivateKeySize {
			return nil, "", fmt.Errorf("privateKey inválida no backup")
		}
		priv = ed25519.PrivateKey(rawKey)
		pubHex = hex.EncodeToString(priv.Public().(ed25519.PublicKey))
	} else {
		// v1.0 do scraper-server: gera keypair novo
		pub, newPriv, err := generateKeyPair()
		if err != nil {
			return nil, "", err
		}
		priv = newPriv
		pubHex = hex.EncodeToString(pub)
	}
	if err := writePrivateKey(priv); err != nil {
		return nil, "", err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	created := backup.User.CreatedAt
	if created == "" {
		created = now
	}
	username := strings.ToLower(strings.TrimSpace(backup.User.Username))
	display := backup.User.DisplayName
	if display == "" {
		display = username
	}
	docID := "user_" + backup.User.ID
	doc := userDoc{
		ID:           docID,
		DocType:      "user",
		UserID:       backup.User.ID,
		Username:     username,
		DisplayName:  display,
		PasswordHash: string(hash),
		PublicKey:    pubHex,
		CreatedAt:    created,
		UpdatedAt:    now,
	}
	if s.useCouch() {
		if existing, rev, err := s.cdb.GetDoc(usersDB, docID); err == nil && len(existing) > 0 {
			doc.Rev = rev
		}
	}
	if err := s.putUser(&doc); err != nil {
		return nil, "", err
	}
	user := publicFromDoc(&doc)
	token, err := s.setSession(user, priv)
	if err != nil {
		return nil, "", err
	}
	if s.buf != nil {
		s.buf.Infof("account", "conta recuperada do backup: %s", username)
	}
	return user, token, nil
}
