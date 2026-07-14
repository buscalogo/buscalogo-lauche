package account

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"buscalogo-agent/internal/paths"
)

func identityDir() (string, error) {
	data, err := paths.Data()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(data, "identity")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func usersDir() (string, error) {
	dir, err := identityDir()
	if err != nil {
		return "", err
	}
	u := filepath.Join(dir, "users")
	if err := os.MkdirAll(u, 0o700); err != nil {
		return "", err
	}
	return u, nil
}

func serverConfigPath() (string, error) {
	dir, err := identityDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "server_config.json"), nil
}

func userFilePath(userID string) (string, error) {
	dir, err := usersDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "user_"+userID+".json"), nil
}

func loadServerIDFile() (string, error) {
	path, err := serverConfigPath()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var doc serverConfigDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	return strings.TrimSpace(doc.ServerID), nil
}

func saveServerIDFile(id string) error {
	path, err := serverConfigPath()
	if err != nil {
		return err
	}
	doc := serverConfigDoc{
		ID:       serverDoc,
		DocType:  "server_config",
		ServerID: id,
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func loadUserFile(userID string) (*userDoc, error) {
	path, err := userFilePath(userID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var doc userDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if doc.DocType != "user" {
		return nil, nil
	}
	if doc.UserID == "" {
		doc.UserID = userID
	}
	doc.ID = "user_" + doc.UserID
	return &doc, nil
}

func saveUserFile(doc *userDoc) error {
	if doc == nil || doc.UserID == "" {
		return fmt.Errorf("userDoc inválido")
	}
	path, err := userFilePath(doc.UserID)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func findUserFileByUsername(username string) (*userDoc, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	dir, err := usersDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "user_") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var doc userDoc
		if json.Unmarshal(raw, &doc) != nil || doc.DocType != "user" {
			continue
		}
		if strings.EqualFold(doc.Username, username) {
			if doc.UserID == "" {
				id := strings.TrimSuffix(strings.TrimPrefix(e.Name(), "user_"), ".json")
				doc.UserID = id
			}
			doc.ID = "user_" + doc.UserID
			return &doc, nil
		}
	}
	return nil, nil
}

func hasUserFile() bool {
	dir, err := usersDir()
	if err != nil {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "user_") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var doc userDoc
		if json.Unmarshal(raw, &doc) == nil && doc.DocType == "user" && doc.UserID != "" {
			return true
		}
	}
	return false
}
