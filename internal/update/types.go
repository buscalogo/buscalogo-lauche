package update

// Status é o estado público do verificador/atualizador.
type Status struct {
	Current     string `json:"current"`
	Latest      string `json:"latest,omitempty"`
	Available   bool   `json:"available"`
	Notes       string `json:"notes,omitempty"`
	State       string `json:"state"` // idle, checking, downloading, ready, installing, done, error
	Progress    int    `json:"progress"`
	Error       string `json:"error,omitempty"`
	DebPath     string `json:"deb_path,omitempty"`
	DebURL      string `json:"deb_url,omitempty"`
	LastCheck   int64  `json:"last_check,omitempty"`
	CanInstall  bool   `json:"can_install"`
	ReleaseURL  string `json:"release_url,omitempty"`
	NeedsRestart bool  `json:"needs_restart,omitempty"`
}

// Manifest é o manifest.json anexado ao GitHub Release.
type Manifest struct {
	Version       string       `json:"version"`
	Notes         string       `json:"notes,omitempty"`
	LinuxAMD64Deb debAsset     `json:"linux_amd64_deb"`
}

type debAsset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Name   string `json:"name,omitempty"`
}
