package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"buscalogo-agent/internal/logx"
)

type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateCrashed  State = "crashed"
	StateDisabled State = "disabled"
)

type Options struct {
	Name        string
	Binary      string
	Args        []string
	Env         []string
	Dir         string
	LogSource   string
	LogBuf      *logx.Buffer
	AutoRestart bool
	StopTimeout time.Duration
	PreStart    func() error // chamado antes de cada spawn (inclusive auto-restart)
}

type Managed struct {
	opts Options
	buf  *logx.Buffer

	mu            sync.Mutex
	cmd           *exec.Cmd
	state         State
	startedAt     time.Time
	restartCount  int64
	stopRequested bool
	watchDone     chan struct{}
	ctx           context.Context
	cancel        context.CancelFunc
}

func New(opts Options) *Managed {
	if opts.StopTimeout == 0 {
		opts.StopTimeout = 10 * time.Second
	}
	if opts.LogBuf == nil {
		opts.LogBuf = logx.NewBuffer(200)
	}
	return &Managed{
		opts:  opts,
		buf:   opts.LogBuf,
		state: StateStopped,
	}
}

func (m *Managed) SetEnv(env []string) {
	m.mu.Lock()
	m.opts.Env = env
	m.mu.Unlock()
}

func (m *Managed) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch m.state {
	case StateRunning, StateStarting, StateStopping:
		return fmt.Errorf("%s já está em execução", m.opts.Name)
	case StateDisabled:
		return fmt.Errorf("%s está desabilitado", m.opts.Name)
	}

	m.stopRequested = false
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.watchDone = make(chan struct{})
	m.state = StateStarting
	m.buf.Infof(m.opts.LogSource, "iniciando %s", m.opts.Name)

	if err := m.spawnLocked(); err != nil {
		m.cancel()
		m.state = StateCrashed
		m.buf.Errorf(m.opts.LogSource, "falha ao iniciar %s: %v", m.opts.Name, err)
		m.watchDone = nil
		return err
	}
	go m.watch()
	return nil
}

func (m *Managed) spawnLocked() error {
	if m.opts.PreStart != nil {
		// PreStart pode chamar SetEnv; solta o lock para evitar deadlock.
		m.mu.Unlock()
		err := m.opts.PreStart()
		m.mu.Lock()
		if err != nil {
			return fmt.Errorf("pre-start: %w", err)
		}
	}
	cmd := exec.Command(m.opts.Binary, m.opts.Args...)
	cmd.Dir = m.opts.Dir
	if len(m.opts.Env) > 0 {
		cmd.Env = m.opts.Env
	}
	cmd.Stdout = logx.SourceWriter{Buffer: m.buf, Source: m.opts.LogSource, Level: "STDOUT"}
	cmd.Stderr = logx.SourceWriter{Buffer: m.buf, Source: m.opts.LogSource, Level: "STDERR"}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:  true,
		Pdeathsig: syscall.SIGTERM, // filho morre se o agente pai morrer (SIGKILL incluído)
	}

	if err := cmd.Start(); err != nil {
		return err
	}
	m.cmd = cmd
	m.startedAt = time.Now()
	m.state = StateRunning
	m.buf.Infof(m.opts.LogSource, "%s iniciado (pid=%d)", m.opts.Name, cmd.Process.Pid)
	return nil
}

func (m *Managed) watch() {
	defer close(m.watchDone)
	for {
		m.mu.Lock()
		cmd := m.cmd
		m.mu.Unlock()
		if cmd == nil {
			return
		}

		err := cmd.Wait()
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}

		m.mu.Lock()
		if m.stopRequested {
			m.state = StateStopped
			m.buf.Infof(m.opts.LogSource, "%s parado (exit=%d)", m.opts.Name, exitCode)
			m.mu.Unlock()
			return
		}

		m.state = StateCrashed
		if err != nil {
			m.buf.Errorf(m.opts.LogSource, "%s caiu (exit=%d): %v", m.opts.Name, exitCode, err)
		} else {
			m.buf.Warnf(m.opts.LogSource, "%s terminou (exit=%d)", m.opts.Name, exitCode)
		}

		if !m.opts.AutoRestart {
			m.mu.Unlock()
			return
		}

		atomic.AddInt64(&m.restartCount, 1)
		backoff := m.backoff()
		m.mu.Unlock()

		m.buf.Infof(m.opts.LogSource, "%s reiniciando em %s", m.opts.Name, backoff)
		select {
		case <-time.After(backoff):
		case <-m.ctx.Done():
			m.mu.Lock()
			if m.stopRequested {
				m.state = StateStopped
			}
			m.mu.Unlock()
			return
		}

		m.mu.Lock()
		if m.stopRequested {
			m.state = StateStopped
			m.mu.Unlock()
			return
		}
		m.state = StateStarting
		if err := m.spawnLocked(); err != nil {
			m.state = StateCrashed
			m.buf.Errorf(m.opts.LogSource, "%s falhou ao reiniciar: %v", m.opts.Name, err)
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
	}
}

func (m *Managed) backoff() time.Duration {
	n := atomic.LoadInt64(&m.restartCount)
	d := time.Duration(n) * 2 * time.Second
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	if d < time.Second {
		d = time.Second
	}
	return d
}

func (m *Managed) Stop() error {
	m.mu.Lock()
	if m.state == StateStopped || m.state == StateDisabled {
		m.mu.Unlock()
		return nil
	}
	m.stopRequested = true
	if m.cancel != nil {
		m.cancel()
	}
	m.state = StateStopping
	cmd := m.cmd
	watchDone := m.watchDone
	m.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	if watchDone != nil {
		select {
		case <-watchDone:
		case <-time.After(m.opts.StopTimeout):
			if cmd != nil && cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			select {
			case <-watchDone:
			case <-time.After(2 * time.Second):
			}
		}
	}
	m.mu.Lock()
	m.cmd = nil
	m.watchDone = nil
	m.state = StateStopped
	m.mu.Unlock()
	return nil
}

func (m *Managed) Restart() error {
	if err := m.Stop(); err != nil {
		return err
	}
	return m.Start()
}

func (m *Managed) Disable() error {
	if err := m.Stop(); err != nil {
		return err
	}
	m.mu.Lock()
	m.state = StateDisabled
	m.mu.Unlock()
	return nil
}

func (m *Managed) Enable() error {
	m.mu.Lock()
	if m.state != StateDisabled {
		m.mu.Unlock()
		return errors.New("serviço não está desabilitado")
	}
	m.state = StateStopped
	m.mu.Unlock()
	return m.Start()
}

type Status struct {
	Name         string `json:"name"`
	State        State  `json:"state"`
	PID          int    `json:"pid"`
	Uptime       int64  `json:"uptime_seconds"`
	RestartCount int64  `json:"restart_count"`
	Binary       string `json:"binary"`
}

// cmdlineMatchesBinary evita falsos positivos quando o nome do binário aparece
// apenas em diretórios do path (ex: epmd em .../couchdb/erts/.../bin/epmd).
func cmdlineMatchesBinary(cmd, binary, binaryName string) bool {
	if strings.Contains(binary, "/") {
		return strings.Contains(cmd, binary)
	}
	if strings.HasPrefix(cmd, binaryName+" ") {
		return true
	}
	return strings.Contains(cmd, "/"+binaryName+" ")
}

// KillExistingByBinary kills any process whose cmdline contains the given binary name
// but is not a system binary (/usr/bin, /usr/local/bin). Used for cleanup of stale
// child processes from previous agent runs.
func KillExistingByBinary(buf *logx.Buffer, name, binary string) error {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}
	binaryName := filepath.Base(binary)
	killed := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if pid == os.Getpid() {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		cmd := strings.ReplaceAll(string(cmdline), "\x00", " ")
		if !cmdlineMatchesBinary(cmd, binary, binaryName) {
			continue
		}
		// Avoid killing system-wide instances.
		if strings.Contains(cmd, "/usr/bin/"+binaryName) || strings.Contains(cmd, "/usr/local/bin/"+binaryName) {
			continue
		}
		if buf != nil {
			buf.Warnf(name, "matando processo antigo %d: %s", pid, cmd)
		}
		if err := KillProcess(pid); err != nil {
			if buf != nil {
				buf.Warnf(name, "falha ao matar %d: %v", pid, err)
			}
		} else {
			killed++
		}
	}
	if killed > 0 {
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

// KillProcess sends SIGINT and then SIGKILL to the given PID.
func KillProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	_ = p.Signal(os.Interrupt)
	time.Sleep(300 * time.Millisecond)
	return p.Kill()
}

func (m *Managed) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	pid := 0
	if m.cmd != nil && m.cmd.Process != nil {
		pid = m.cmd.Process.Pid
	}
	uptime := int64(0)
	if m.state == StateRunning && !m.startedAt.IsZero() {
		uptime = int64(time.Since(m.startedAt).Seconds())
	}
	return Status{
		Name:         m.opts.Name,
		State:        m.state,
		PID:          pid,
		Uptime:       uptime,
		RestartCount: atomic.LoadInt64(&m.restartCount),
		Binary:       m.opts.Binary,
	}
}
