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

func TestStartIdempotent(t *testing.T) {
	m := New(Options{
		Name:        "test-idem",
		Binary:      "sleep",
		Args:        []string{"5"},
		LogSource:   "test",
		LogBuf:      logx.NewBuffer(100),
		AutoRestart: false,
		StopTimeout: 2 * time.Second,
	})
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid1 := m.Status().PID
	if err := m.Start(); err != nil {
		t.Fatalf("Start idempotente: %v", err)
	}
	if m.Status().PID != pid1 {
		t.Fatalf("PID mudou no segundo Start: %d -> %d", pid1, m.Status().PID)
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestStartSupersedesAutoRestartWatch(t *testing.T) {
	m := New(Options{
		Name:        "test-supersede",
		Binary:      "sh",
		Args:        []string{"-c", "exit 0"},
		LogSource:   "test",
		LogBuf:      logx.NewBuffer(100),
		AutoRestart: true,
		StopTimeout: 2 * time.Second,
	})
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m.Status().RestartCount > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if m.Status().RestartCount == 0 {
		_ = m.Stop()
		t.Fatal("esperado AutoRestart ter entrado em backoff")
	}

	m.mu.Lock()
	m.opts.Binary = "sleep"
	m.opts.Args = []string{"5"}
	m.mu.Unlock()

	if err := m.Start(); err != nil {
		t.Fatalf("Start após crash: %v", err)
	}

	pid := m.Status().PID
	time.Sleep(800 * time.Millisecond)
	st := m.Status()
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if st.State != StateRunning {
		t.Fatalf("esperado running estável, got %s restart=%d", st.State, st.RestartCount)
	}
	if st.PID != pid {
		t.Fatalf("watch antigo matou o processo: pid %d -> %d", pid, st.PID)
	}
}
