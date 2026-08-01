package update

// Product identifica o binário atualizado via GitHub Releases.
const (
	ProductAgent    = "agent"
	ProductRegistry = "registry"
)

// Status é o estado público do verificador/atualizador.
type Status struct {
	Current      string `json:"current"`
	Latest       string `json:"latest,omitempty"`
	Available    bool   `json:"available"`
	Notes        string `json:"notes,omitempty"`
	State        string `json:"state"` // idle, checking, downloading, ready, installing, done, error
	Progress     int    `json:"progress"`
	Error        string `json:"error,omitempty"`
	DebPath      string `json:"deb_path,omitempty"` // path do artefato baixado (.deb ou binário registry)
	DebURL       string `json:"deb_url,omitempty"`  // URL do artefato
	LastCheck    int64  `json:"last_check,omitempty"`
	CanInstall   bool   `json:"can_install"`
	ReleaseURL   string `json:"release_url,omitempty"`
	NeedsRestart bool   `json:"needs_restart,omitempty"`
	Product      string `json:"product,omitempty"`
}

// Manifest é o manifest.json anexado ao GitHub Release.
type Manifest struct {
	Version            string   `json:"version"`
	Notes              string   `json:"notes,omitempty"`
	LinuxAMD64Deb      debAsset `json:"linux_amd64_deb"`
	LinuxAMD64Registry debAsset `json:"linux_amd64_registry"`
	LinuxARM64Registry debAsset `json:"linux_arm64_registry"`
	WindowsAMD64MSI    debAsset `json:"windows_amd64_msi,omitempty"`
}

type debAsset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Name   string `json:"name,omitempty"`
}
