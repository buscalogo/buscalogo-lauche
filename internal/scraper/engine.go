package scraper

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"

	"buscalogo-agent/internal/logx"
)

type Engine struct {
	cfg     RuntimeConfig
	store   *Store
	buf     *logx.Buffer
	client  *http.Client

	mu            sync.Mutex
	queues        map[Priority][]*Task
	active        map[string]*Task
	processed     map[string]struct{}
	metrics       queueMetrics
	startedAt     time.Time
	running       bool
	stopCh          chan struct{}
	processWake     chan struct{}
	currentConcurrent int
}

type queueMetrics struct {
	totalProcessed    int64
	successful        int64
	failed            int64
	linksDiscovered   int64
	avgProcessingTime float64
}

func NewEngine(cfg RuntimeConfig, store *Store, buf *logx.Buffer) *Engine {
	return &Engine{
		cfg:         cfg,
		store:       store,
		buf:         buf,
		client:      &http.Client{Timeout: 30 * time.Second},
		queues:      map[Priority][]*Task{PriorityHigh: {}, PriorityNormal: {}, PriorityLow: {}, PriorityDiscovered: {}},
		active:      map[string]*Task{},
		processed:   map[string]struct{}{},
		processWake: make(chan struct{}, 1),
	}
}

func (e *Engine) Start() {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return
	}
	e.running = true
	e.startedAt = time.Now()
	e.stopCh = make(chan struct{})
	e.mu.Unlock()
	go e.loop()
	e.wake()
}

func (e *Engine) Stop() {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return
	}
	e.running = false
	close(e.stopCh)
	e.mu.Unlock()
}

func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

func (e *Engine) SetConfig(cfg RuntimeConfig) {
	e.mu.Lock()
	e.cfg = cfg
	e.mu.Unlock()
}

func (e *Engine) AddToQueue(rawURL string, priority Priority, depth, maxDepth, scheduleDays int, discoveredFrom, taskType string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("URL inválida")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("apenas http/https")
	}
	abs := u.String()

	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.processed[abs]; ok {
		return "", nil
	}
	if e.isInQueueLocked(abs) {
		return "", nil
	}
	if blocked(u.Hostname(), e.cfg.BlockedDomains) {
		return "", fmt.Errorf("domínio bloqueado")
	}
	if priority == "" {
		priority = PriorityNormal
	}
	if maxDepth <= 0 {
		maxDepth = e.cfg.MaxDepth
	}
	if scheduleDays <= 0 {
		scheduleDays = e.cfg.DefaultScheduleDays
	}
	task := &Task{
		ID:             newTaskID(),
		URL:            abs,
		Priority:       priority,
		DiscoveredFrom: discoveredFrom,
		Depth:          depth,
		MaxDepth:       maxDepth,
		Type:           taskType,
		Status:         "queued",
		Hostname:       u.Hostname(),
		ScheduleDays:   scheduleDays,
		CreatedAt:      time.Now().UnixMilli(),
	}
	if task.Type == "" {
		task.Type = "manual"
	}
	e.queues[priority] = append(e.queues[priority], task)
	e.buf.Infof("scraper", "enfileirado [%s] %s", priority, abs)
	e.wakeLocked()
	return task.ID, nil
}

func (e *Engine) ClearQueue() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for p := range e.queues {
		e.queues[p] = nil
	}
}

func (e *Engine) Stats() QueueStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	var s QueueStats
	s.Queues.High = len(e.queues[PriorityHigh])
	s.Queues.Normal = len(e.queues[PriorityNormal])
	s.Queues.Low = len(e.queues[PriorityLow])
	s.Queues.Discovered = len(e.queues[PriorityDiscovered])
	s.Active = len(e.active)
	s.Processed = len(e.processed)
	s.Metrics.TotalProcessed = e.metrics.totalProcessed
	s.Metrics.Successful = e.metrics.successful
	s.Metrics.Failed = e.metrics.failed
	s.Metrics.LinksDiscovered = e.metrics.linksDiscovered
	s.Metrics.AvgProcessingTime = e.metrics.avgProcessingTime
	if e.running {
		s.UptimeMs = time.Since(e.startedAt).Milliseconds()
	}
	s.Running = e.running
	return s
}

func (e *Engine) TasksSnapshot() (active, queued []*Task) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.active {
		active = append(active, cloneTask(t))
	}
	order := []Priority{PriorityHigh, PriorityNormal, PriorityLow, PriorityDiscovered}
	for _, p := range order {
		for _, t := range e.queues[p] {
			queued = append(queued, cloneTask(t))
		}
	}
	return active, queued
}

func cloneTask(t *Task) *Task {
	c := *t
	return &c
}

func (e *Engine) loop() {
	for {
		select {
		case <-e.stopCh:
			return
		case <-e.processWake:
			e.processAvailable()
		}
	}
}

func (e *Engine) wake() {
	select {
	case e.processWake <- struct{}{}:
	default:
	}
}

func (e *Engine) wakeLocked() { e.wake() }

func (e *Engine) processAvailable() {
	for {
		e.mu.Lock()
		if !e.running || e.currentConcurrent >= e.cfg.MaxConcurrent {
			e.mu.Unlock()
			return
		}
		task := e.nextTaskLocked()
		if task == nil {
			e.mu.Unlock()
			return
		}
		e.currentConcurrent++
		e.mu.Unlock()
		go func(t *Task) {
			e.runTask(t)
			e.mu.Lock()
			e.currentConcurrent--
			e.mu.Unlock()
			e.wake()
		}(task)
	}
}

func (e *Engine) nextTaskLocked() *Task {
	for _, p := range []Priority{PriorityHigh, PriorityNormal, PriorityLow, PriorityDiscovered} {
		q := e.queues[p]
		if len(q) == 0 {
			continue
		}
		task := q[0]
		e.queues[p] = q[1:]
		return task
	}
	return nil
}

func (e *Engine) isInQueueLocked(u string) bool {
	for _, q := range e.queues {
		for _, t := range q {
			if t.URL == u {
				return true
			}
		}
	}
	return false
}

func (e *Engine) runTask(task *Task) {
	e.mu.Lock()
	task.Status = "running"
	task.StartedAt = time.Now().UnixMilli()
	task.Progress = 10
	e.active[task.ID] = task
	e.mu.Unlock()

	err := e.execute(task)

	e.mu.Lock()
	delete(e.active, task.ID)
	e.processed[task.URL] = struct{}{}
	e.metrics.totalProcessed++
	e.mu.Unlock()

	if err != nil {
		e.handleFailure(task, err)
		return
	}
}

func (e *Engine) execute(task *Task) error {
	req, err := http.NewRequest(http.MethodGet, task.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", pickUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "pt-BR,pt;q=0.9,en;q=0.8")

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return err
	}

	content := extractPage(doc, task.URL)
	discovered := discoverLinks(content, task, e.cfg)
	analysis := analyzeContent(content, task.URL)
	started := task.StartedAt
	result := ScrapeResult{
		TaskID:          task.ID,
		URL:             task.URL,
		Content:         content,
		DiscoveredLinks: discovered,
		Analysis:        analysis,
		Metadata: map[string]any{
			"scraped_at":       time.Now().UTC().Format(time.RFC3339),
			"depth":            task.Depth,
			"discovered_from":  task.DiscoveredFrom,
			"processing_time":  time.Now().UnixMilli() - started,
			"http_status":      resp.StatusCode,
			"content_type":     resp.Header.Get("Content-Type"),
		},
	}

	if e.store != nil {
		if err := e.store.Save(task, result, task.ScheduleDays); err != nil {
			e.buf.Warnf("scraper", "persistir %s: %v", task.URL, err)
		}
	}

	internal := 0
	for _, l := range discovered {
		if l.IsInternal {
			internal++
		}
	}
	maxDepth := task.MaxDepth
	if maxDepth <= 0 {
		maxDepth = e.cfg.MaxDepth
	}
	if task.Depth < maxDepth && internal > 0 {
		for _, link := range discovered {
			if !link.IsInternal {
				continue
			}
			_, _ = e.AddToQueue(link.Href, Priority(link.Priority), task.Depth+1, maxDepth, task.ScheduleDays, task.URL, "discovered")
		}
		e.mu.Lock()
		e.metrics.linksDiscovered += int64(internal)
		e.mu.Unlock()
	}

	e.mu.Lock()
	task.Status = "completed"
	task.Progress = 100
	task.CompletedAt = time.Now().UnixMilli()
	e.metrics.successful++
	dur := float64(time.Now().UnixMilli() - started)
	n := float64(e.metrics.successful)
	e.metrics.avgProcessingTime = ((e.metrics.avgProcessingTime * (n - 1)) + dur) / n
	e.mu.Unlock()
	e.buf.Infof("scraper", "concluído %s (%dms)", task.URL, int(dur))
	return nil
}

func (e *Engine) handleFailure(task *Task, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	task.Error = err.Error()
	if task.RetryCount < e.cfg.MaxRetries {
		task.RetryCount++
		task.Status = "retrying"
		delay := e.cfg.RequestDelay * time.Duration(1<<task.RetryCount)
		taskCopy := cloneTask(task)
		go func() {
			time.Sleep(delay)
			e.mu.Lock()
			e.queues[PriorityLow] = append(e.queues[PriorityLow], taskCopy)
			e.mu.Unlock()
			e.wake()
		}()
		e.buf.Warnf("scraper", "retry %s: %v", task.URL, err)
		return
	}
	task.Status = "failed"
	task.CompletedAt = time.Now().UnixMilli()
	e.metrics.failed++
	e.buf.Errorf("scraper", "falha %s: %v", task.URL, err)
}

func newTaskID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("task_%d_%s", time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}

func pickUserAgent() string {
	if len(userAgents) == 0 {
		return "BuscaLogo-Agent/1.0"
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(userAgents))))
	return userAgents[n.Int64()]
}
