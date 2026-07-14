package paths

import "testing"

func TestIsDaemonBinary(t *testing.T) {
	cases := map[string]bool{
		"buscalogo-agentd":     true,
		"buscalogo-agentd.exe": true,
		"BUSCALOGO-AGENTD.EXE": true,
		"buscalogo-agent":      false,
		"buscalogo-agent.exe":  false,
	}
	for name, want := range cases {
		if got := isDaemonBinary(name); got != want {
			t.Errorf("isDaemonBinary(%q)=%v want %v", name, got, want)
		}
	}
}
