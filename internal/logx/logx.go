package logx

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Entry struct {
	ID      int64     `json:"id"`
	Time    time.Time `json:"time"`
	Source  string    `json:"source"`  // agent | coredns | yggdrasil | dns | system
	Level   string    `json:"level"`   // INFO | WARN | ERROR | STDOUT | STDERR
	Message string    `json:"message"`
}

type Buffer struct {
	mu      sync.RWMutex
	entries []Entry
	cap     int
	next    int64
	subs    map[chan Entry]struct{}
}

func NewBuffer(cap int) *Buffer {
	if cap <= 0 {
		cap = 1000
	}
	return &Buffer{
		entries: make([]Entry, 0, cap),
		cap:     cap,
		subs:    make(map[chan Entry]struct{}),
	}
}

func (b *Buffer) Append(source, level, msg string) Entry {
	e := Entry{
		ID:      atomic.AddInt64(&b.next, 1),
		Time:    time.Now(),
		Source:  source,
		Level:   strings.TrimSpace(level),
		Message: strings.TrimRight(msg, "\n"),
	}

	b.mu.Lock()
	if len(b.entries) >= b.cap {
		b.entries = b.entries[1:]
	}
	b.entries = append(b.entries, e)
	subs := make([]chan Entry, 0, len(b.subs))
	for ch := range b.subs {
		subs = append(subs, ch)
	}
	b.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- e:
		default:
		}
	}
	return e
}

func (b *Buffer) Recent(n int) []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if n <= 0 || n > len(b.entries) {
		n = len(b.entries)
	}
	out := make([]Entry, n)
	copy(out, b.entries[len(b.entries)-n:])
	return out
}

func (b *Buffer) Subscribe(ctx context.Context) (<-chan Entry, func()) {
	ch := make(chan Entry, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
		close(ch)
	}
}

// SourceWriter adapta um Buffer para capturar stdout/stderr de um processo.
type SourceWriter struct {
	Buffer *Buffer
	Source string
	Level  string
}

func (w SourceWriter) Write(p []byte) (int, error) {
	r := bufio.NewReader(strings.NewReader(string(p)))
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			w.Buffer.Append(w.Source, w.Level, strings.TrimRight(line, "\n"))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return len(p), err
		}
	}
	return len(p), nil
}

func (b *Buffer) Infof(source, format string, args ...any) {
	b.Append(source, "INFO", fmt.Sprintf(format, args...))
}
func (b *Buffer) Warnf(source, format string, args ...any) {
	b.Append(source, "WARN", fmt.Sprintf(format, args...))
}
func (b *Buffer) Errorf(source, format string, args ...any) {
	b.Append(source, "ERROR", fmt.Sprintf(format, args...))
}

// Write captura texto bruto (ex.: saída de comandos privilegiados) linha a linha.
func (b *Buffer) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			b.Append("system", "INFO", line)
		}
	}
	return len(p), nil
}
