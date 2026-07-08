package scraper

import (
	"net/url"
	"strings"
	"unicode"
)

func analyzeContent(content PageContent, pageURL string) Analysis {
	main := content.MainText
	words := strings.Fields(main)
	a := Analysis{
		WordCount:   len(words),
		Readability: readabilityScore(main),
		Topics:      extractTopics(content),
		Sentiment:   analyzeSentiment(main),
		Language:    detectLanguage(main),
		ContentType: detectContentType(content),
	}
	for _, l := range content.Links {
		if isSameHost(pageURL, l.Href) {
			a.InternalLinks++
		} else {
			a.ExternalLinks++
		}
	}
	return a
}

func readabilityScore(text string) string {
	sentences := len(splitSentences(text))
	if sentences == 0 {
		return "medium"
	}
	words := len(strings.Fields(text))
	avg := float64(words) / float64(sentences)
	switch {
	case avg < 10:
		return "easy"
	case avg < 15:
		return "medium"
	default:
		return "hard"
	}
}

func splitSentences(text string) []string {
	var parts []string
	var b strings.Builder
	for _, r := range text {
		b.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			parts = append(parts, strings.TrimSpace(b.String()))
			b.Reset()
		}
	}
	if s := strings.TrimSpace(b.String()); s != "" {
		parts = append(parts, s)
	}
	return parts
}

func extractTopics(content PageContent) []string {
	all := strings.ToLower(strings.Join([]string{
		content.Title,
		content.Description,
		strings.Join(content.Headings.H1, " "),
		strings.Join(content.Headings.H2, " "),
		strings.Join(content.Paragraphs, " "),
	}, " "))
	stop := map[string]bool{
		"este": true, "esse": true, "aquele": true, "para": true, "qual": true,
		"quer": true, "todo": true, "mais": true, "com": true, "uma": true,
	}
	freq := map[string]int{}
	for _, w := range strings.Fields(all) {
		w = strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) })
		if len(w) < 4 || stop[w] {
			continue
		}
		freq[w]++
	}
	type kv struct {
		k string
		v int
	}
	var list []kv
	for k, v := range freq {
		list = append(list, kv{k, v})
	}
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[j].v > list[i].v {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	limit := 10
	if len(list) < limit {
		limit = len(list)
	}
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, list[i].k)
	}
	return out
}

func analyzeSentiment(text string) string {
	pos := []string{"bom", "ótimo", "excelente", "perfeito", "maravilhoso", "feliz"}
	neg := []string{"ruim", "péssimo", "terrível", "horrível", "triste", "raiva"}
	lower := strings.ToLower(text)
	p, n := 0, 0
	for _, w := range pos {
		if strings.Contains(lower, w) {
			p++
		}
	}
	for _, w := range neg {
		if strings.Contains(lower, w) {
			n++
		}
	}
	switch {
	case p > n:
		return "positive"
	case n > p:
		return "negative"
	default:
		return "neutral"
	}
}

func detectLanguage(text string) string {
	sample := strings.ToLower(text)
	pt := []string{"que", "de", "para", "com", "não", "uma", "dos", "como"}
	en := []string{"the", "and", "for", "with", "that", "this", "from", "have"}
	ptC, enC := 0, 0
	for _, w := range strings.Fields(sample) {
		for _, p := range pt {
			if w == p {
				ptC++
			}
		}
		for _, e := range en {
			if w == e {
				enC++
			}
		}
	}
	switch {
	case ptC > enC:
		return "pt"
	case enC > ptC:
		return "en"
	default:
		return "unknown"
	}
}

func detectContentType(content PageContent) string {
	text := strings.ToLower(content.Title + " " + content.Description)
	switch {
	case strings.Contains(text, "produto"), strings.Contains(text, "comprar"), strings.Contains(text, "preço"):
		return "ecommerce"
	case strings.Contains(text, "notícia"), strings.Contains(text, "jornal"):
		return "news"
	case strings.Contains(text, "blog"), strings.Contains(text, "artigo"):
		return "blog"
	case strings.Contains(text, "fórum"), strings.Contains(text, "pergunta"):
		return "forum"
	default:
		return "general"
	}
}

func isSameHost(baseURL, link string) bool {
	b, err1 := url.Parse(baseURL)
	l, err2 := url.Parse(link)
	if err1 != nil || err2 != nil {
		return false
	}
	return baseDomain(b.Hostname()) == baseDomain(l.Hostname())
}

func baseDomain(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) <= 2 {
		return host
	}
	last := parts[len(parts)-1]
	second := parts[len(parts)-2]
	multi := (last == "br" || last == "uk") && (second == "com" || second == "co" || second == "org")
	if multi && len(parts) >= 3 {
		return strings.Join(parts[len(parts)-3:], ".")
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func extractSearchableText(result ScrapeResult) string {
	c := result.Content
	paras := c.Paragraphs
	if len(paras) > 20 {
		paras = paras[:20]
	}
	main := c.MainText
	if len(main) > 5000 {
		main = main[:5000]
	}
	return strings.TrimSpace(strings.Join([]string{
		c.Title,
		c.Description,
		strings.Join(c.Headings.H1, " "),
		strings.Join(c.Headings.H2, " "),
		strings.Join(paras, " "),
		main,
	}, " "))
}

func extractTerms(text string, minLen int) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.Fields(strings.ToLower(text)) {
		w = strings.TrimFunc(w, func(r rune) bool {
			return !(unicode.IsLetter(r) || unicode.IsNumber(r))
		})
		if len(w) < minLen || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}
