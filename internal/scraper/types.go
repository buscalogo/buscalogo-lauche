package scraper

import "time"

const (
	scrapingDBLegacy = "buscalogo_scraping"
	hostDBPrefix     = "bl_scraping_"
	siteMetaDocID    = "bl_site_meta"

	// Caps de persistência — evita docs de dezenas/centenas de KB.
	maxStoredTextBytes   = 16_000
	maxMainTextBytes     = 12_000
	maxParagraphs        = 20
	maxParagraphChars    = 500
	maxStoredImages      = 10
	maxStoredTerms       = 200
)

type Priority string

const (
	PriorityHigh       Priority = "high"
	PriorityNormal     Priority = "normal"
	PriorityLow        Priority = "low"
	PriorityDiscovered Priority = "discovered"
)

type Task struct {
	ID             string   `json:"id"`
	URL            string   `json:"url"`
	Priority       Priority `json:"priority"`
	DiscoveredFrom string   `json:"discovered_from,omitempty"`
	Depth          int      `json:"depth"`
	MaxDepth       int      `json:"max_depth"`
	Type           string   `json:"type"`
	Status         string   `json:"status"`
	Progress       int      `json:"progress"`
	RetryCount     int      `json:"retry_count"`
	Hostname       string   `json:"hostname"`
	ScheduleDays   int      `json:"schedule_days"`
	Error          string   `json:"error,omitempty"`
	CreatedAt      int64    `json:"created_at"`
	StartedAt      int64    `json:"started_at,omitempty"`
	CompletedAt    int64    `json:"completed_at,omitempty"`
}

type QueueStats struct {
	Queues struct {
		High       int `json:"high"`
		Normal     int `json:"normal"`
		Low        int `json:"low"`
		Discovered int `json:"discovered"`
	} `json:"queues"`
	Active    int `json:"active"`
	Processed int `json:"processed"`
	Metrics   struct {
		TotalProcessed    int64   `json:"total_processed"`
		Successful        int64   `json:"successful"`
		Failed            int64   `json:"failed"`
		LinksDiscovered   int64   `json:"links_discovered"`
		AvgProcessingTime float64 `json:"avg_processing_time_ms"`
	} `json:"metrics"`
	UptimeMs int64 `json:"uptime_ms"`
	Running  bool  `json:"running"`
}

type RuntimeConfig struct {
	MaxConcurrent        int
	MaxDepth             int
	MaxRetries           int
	RequestDelay         time.Duration
	MaxLinksPerPage      int
	DiscoverInternalOnly bool
	DefaultScheduleDays  int
	BlockedDomains       []string
	AllowedDomains       []string
}

type ScrapedLink struct {
	Href       string `json:"href"`
	Text       string `json:"text"`
	Title      string `json:"title,omitempty"`
	IsInternal bool   `json:"is_internal"`
	Priority   string `json:"priority,omitempty"`
}

type PageContent struct {
	URL         string            `json:"url"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Keywords    string            `json:"keywords"`
	Author      string            `json:"author"`
	Meta        map[string]string `json:"meta"`
	Headings    struct {
		H1 []string `json:"h1"`
		H2 []string `json:"h2"`
		H3 []string `json:"h3"`
	} `json:"headings"`
	Paragraphs  []string      `json:"paragraphs"`
	MainText    string        `json:"main_text"`
	Links       []ScrapedLink `json:"links"`
	Images      []struct {
		Src   string `json:"src"`
		Alt   string `json:"alt"`
		Title string `json:"title"`
	} `json:"images"`
	Favicon     string `json:"favicon"`
	ExtractedAt string `json:"extracted_at"`
}

type Analysis struct {
	WordCount       int      `json:"word_count"`
	Readability     string   `json:"readability"`
	Topics          []string `json:"topics"`
	Sentiment       string   `json:"sentiment"`
	Language        string   `json:"language"`
	ContentType     string   `json:"content_type"`
	InternalLinks   int      `json:"internal_links"`
	ExternalLinks   int      `json:"external_links"`
}

type ScrapeResult struct {
	TaskID          string        `json:"task_id"`
	URL             string        `json:"url"`
	Content         PageContent   `json:"content"`
	DiscoveredLinks []ScrapedLink `json:"discovered_links"`
	Analysis        Analysis      `json:"analysis"`
	Metadata        map[string]any `json:"metadata"`
}

type StoredDoc struct {
	ID              string         `json:"_id"`
	Rev             string         `json:"_rev,omitempty"`
	TaskID          string         `json:"task_id"`
	URL             string         `json:"url"`
	Title           string         `json:"title"`
	Description     string         `json:"description"`
	Text            string         `json:"text"`
	Terms           []string       `json:"terms"`
	Hostname        string         `json:"hostname"`
	Content         PageContent    `json:"content"`
	Analysis        Analysis       `json:"analysis"`
	DiscoveredLinks []ScrapedLink  `json:"discovered_links"`
	InternalLinks   []ScrapedLink  `json:"internal_links"`
	Metadata        map[string]any `json:"metadata"`
	CreatedAt       string         `json:"created_at"`
	UpdatedAt       string         `json:"updated_at"`
	NextCheckAt     *string        `json:"next_check_at"`
	ScheduleDays    int            `json:"schedule_days"`
	DocType         string         `json:"doc_type"`
	CreatedBy       any            `json:"created_by,omitempty"`
	Signature       string         `json:"signature,omitempty"`
	SignedAt        string         `json:"signed_at,omitempty"`
}
