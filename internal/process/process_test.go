package process

import (
	"testing"
	"time"

	"buscalogo-agent/internal/logx"
)

func TestStartStopStatus(t *testing.T) {
	m := New(Options{
		Name:       "test-sleep",
		Binary:     "sleep",
		Args:       []string{"30"},
		LogSource:  "test",
		LogBuf:     logx.NewBuffer(100),
		AutoRestart: false,
	})

	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	st := m.Status()
	if st.State != StateRunning {
		t.Fatalf("esperado running, got %s (pid=%d)", st.State, st.PID)
	}
	if st.PID == 0 {
		t.Fatal("PID não deve ser 0 em running")
	}

	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	st = m.Status()
	if st.State != StateStopped {
		t.Fatalf("esperado stopped, got %s", st.State)
	}
}

func TestAutoRestart(t *testing.T) {
	m := New(Options{
		Name:        "test-crash",
		Binary:      "sh",
		Args:        []string{"-c", "echo start; exit 1"},
		LogSource:   "test",
		LogBuf:      logx.NewBuffer(100),
		AutoRestart: true,
	})
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer m.Stop()

	time.Sleep(2500 * time.Millisecond)
	st := m.Status()
	if st.RestartCount == 0 {
		t.Fatalf("esperado restart_count > 0, got %d (state=%s)", st.RestartCount, st.State)
	}
	if st.State != StateRunning && st.State != StateStarting && st.State != StateCrashed {
		t.Fatalf("estado inesperado após restarts: %s", st.State)
	}
}
