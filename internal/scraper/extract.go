package scraper

import (
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
}

func extractPage(doc *goquery.Document, pageURL string) PageContent {
	content := PageContent{
		URL:  pageURL,
		Meta: map[string]string{},
	}
	content.Title = strings.TrimSpace(doc.Find("title").First().Text())
	doc.Find("meta[name], meta[property]").Each(func(_ int, s *goquery.Selection) {
		name, _ := s.Attr("name")
		if name == "" {
			name, _ = s.Attr("property")
		}
		val, _ := s.Attr("content")
		if name != "" && val != "" {
			content.Meta[name] = val
		}
	})
	content.Description = content.Meta["description"]
	content.Keywords = content.Meta["keywords"]
	content.Author = content.Meta["author"]

	doc.Find("h1").Each(func(_ int, s *goquery.Selection) {
		content.Headings.H1 = append(content.Headings.H1, strings.TrimSpace(s.Text()))
	})
	doc.Find("h2").Each(func(_ int, s *goquery.Selection) {
		content.Headings.H2 = append(content.Headings.H2, strings.TrimSpace(s.Text()))
	})
	doc.Find("h3").Each(func(_ int, s *goquery.Selection) {
		content.Headings.H3 = append(content.Headings.H3, strings.TrimSpace(s.Text()))
	})
	doc.Find("p").Each(func(_ int, s *goquery.Selection) {
		t := strings.TrimSpace(s.Text())
		if t != "" {
			content.Paragraphs = append(content.Paragraphs, t)
		}
	})
	content.MainText = strings.TrimSpace(doc.Find("body").Text())

	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		title, _ := s.Attr("title")
		content.Links = append(content.Links, ScrapedLink{
			Href:  href,
			Text:  strings.TrimSpace(s.Text()),
			Title: title,
		})
	})
	doc.Find("img").Each(func(_ int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		alt, _ := s.Attr("alt")
		title, _ := s.Attr("title")
		content.Images = append(content.Images, struct {
			Src   string `json:"src"`
			Alt   string `json:"alt"`
			Title string `json:"title"`
		}{Src: src, Alt: alt, Title: title})
	})

	base, _ := url.Parse(pageURL)
	doc.Find(`link[rel="icon"], link[rel="shortcut icon"], link[rel="apple-touch-icon"]`).Each(func(_ int, s *goquery.Selection) {
		if content.Favicon != "" {
			return
		}
		if href, ok := s.Attr("href"); ok && href != "" && base != nil {
			if u, err := base.Parse(href); err == nil {
				content.Favicon = u.String()
			}
		}
	})
	if content.Favicon == "" && base != nil {
		content.Favicon = base.Scheme + "://" + base.Host + "/favicon.ico"
	}
	content.ExtractedAt = time.Now().UTC().Format(time.RFC3339)
	return content
}

func discoverLinks(content PageContent, task *Task, cfg RuntimeConfig) []ScrapedLink {
	current, err := url.Parse(task.URL)
	if err != nil {
		return nil
	}
	currentBase := baseDomain(current.Hostname())
	var out []ScrapedLink
	for _, link := range content.Links {
		if len(out) >= cfg.MaxLinksPerPage {
			break
		}
		abs, err := current.Parse(link.Href)
		if err != nil || (abs.Scheme != "http" && abs.Scheme != "https") {
			continue
		}
		link.Href = abs.String()
		if link.Href == task.URL {
			continue
		}
		host := abs.Hostname()
		if blocked(host, cfg.BlockedDomains) {
			continue
		}
		if len(cfg.AllowedDomains) > 0 && !contains(cfg.AllowedDomains, host) {
			continue
		}
		linkBase := baseDomain(host)
		link.IsInternal = linkBase == currentBase
		if cfg.DiscoverInternalOnly && !link.IsInternal {
			continue
		}
		if link.IsInternal {
			link.Priority = string(PriorityNormal)
		} else {
			link.Priority = string(PriorityLow)
		}
		out = append(out, link)
	}
	return out
}

func blocked(host string, list []string) bool {
	return contains(list, host)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
