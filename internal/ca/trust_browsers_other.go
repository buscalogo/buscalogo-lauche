//go:build !unix

package ca

type UserTrustResult struct {
	Browsers []string `json:"browsers,omitempty"`
	Apps     []string `json:"apps,omitempty"`
	EnvFile  string   `json:"env_file,omitempty"`
	CertFile string   `json:"cert_file,omitempty"`
	Bundle   string   `json:"bundle,omitempty"`
}

func BrowserTrustInstalled() bool { return false }

func BrowserTrustProfiles() []string { return nil }

func AppTrustInstalled() bool { return false }

func InstallBrowserTrust(_ systemAppender, _ []byte) ([]string, error) {
	return nil, nil
}

func InstallUserTrust(_ systemAppender, _ []byte) (UserTrustResult, error) {
	return UserTrustResult{}, nil
}
