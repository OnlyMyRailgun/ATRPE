package research

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// FetchedPage represents a successfully retrieved web page.
type FetchedPage struct {
	URL         string
	Title       string
	Content     string // cleaned text, truncated to ~8K tokens
	ContentHash string // sha256[:16]
	RetrievedAt time.Time
	StatusCode  int
	Error       string // empty if successful
}

// WebFetcher retrieves and cleans web pages for LLM consumption.
type WebFetcher interface {
	Fetch(ctx context.Context, url string) (*FetchedPage, error)
	FetchMultiple(ctx context.Context, urls []string, concurrency int) ([]*FetchedPage, error)
}

// DefaultWebFetcher implements WebFetcher using net/http.
type DefaultWebFetcher struct {
	client  *http.Client
	maxLen  int // max content length in bytes (~32KB = ~8K tokens)
}

// NewWebFetcher creates a fetcher with sensible defaults.
func NewWebFetcher() *DefaultWebFetcher {
	return &DefaultWebFetcher{
		client: &http.Client{Timeout: 15 * time.Second},
		maxLen: 32 * 1024, // ~8K tokens
	}
}

// Fetch retrieves and cleans a single page.
func (f *DefaultWebFetcher) Fetch(ctx context.Context, url string) (*FetchedPage, error) {
	page := &FetchedPage{
		URL:         url,
		RetrievedAt: time.Now().UTC(),
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		page.Error = err.Error()
		return page, err
	}
	req.Header.Set("User-Agent", "ATRPE/1.0 (research-fetcher)")

	resp, err := f.client.Do(req)
	if err != nil {
		page.Error = err.Error()
		return page, err
	}
	defer resp.Body.Close()

	page.StatusCode = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		page.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return page, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	// Read and clean HTML
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(f.maxLen*2))) // read up to 2x for parsing
	if err != nil {
		page.Error = err.Error()
		return page, err
	}

	page.Title = extractTitle(string(raw))
	page.Content = cleanHTML(string(raw), f.maxLen)
	page.ContentHash = hashString(page.Content)[:16]

	return page, nil
}

// FetchMultiple fetches multiple URLs concurrently.
func (f *DefaultWebFetcher) FetchMultiple(ctx context.Context, urls []string, concurrency int) ([]*FetchedPage, error) {
	if concurrency <= 0 {
		concurrency = 3
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []*FetchedPage
		sem     = make(chan struct{}, concurrency)
		errs    []error
	)

	for _, url := range urls {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			page, err := f.Fetch(ctx, u)
			mu.Lock()
			if err != nil {
				errs = append(errs, err)
			}
			if page != nil {
				results = append(results, page)
			}
			mu.Unlock()
		}(url)
	}
	wg.Wait()

	if len(results) == 0 && len(errs) > 0 {
		return results, fmt.Errorf("all %d fetches failed; first error: %w", len(urls), errs[0])
	}

	return results, nil
}

// cleanHTML extracts readable text from HTML, stripping tags, scripts, and styles.
func cleanHTML(htmlStr string, maxLen int) string {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		// Fallback: strip tags naively
		return truncateString(naiveStripTags(htmlStr), maxLen)
	}

	var sb strings.Builder
	var extractText func(*html.Node)
	skipTags := map[string]bool{"script": true, "style": true, "noscript": true, "svg": true, "head": true}

	extractText = func(n *html.Node) {
		if n.Type == html.ElementNode && skipTags[n.Data] {
			return
		}
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				if sb.Len() > 0 {
					sb.WriteString(" ")
				}
				sb.WriteString(text)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extractText(c)
		}
	}
	extractText(doc)

	return truncateString(sb.String(), maxLen)
}

func extractTitle(htmlStr string) string {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return ""
	}
	var title string
	var findTitle func(*html.Node)
	findTitle = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "title" && n.FirstChild != nil {
			title = strings.TrimSpace(n.FirstChild.Data)
			return
		}
		for c := n.FirstChild; c != nil && title == ""; c = c.NextSibling {
			findTitle(c)
		}
	}
	findTitle(doc)
	return title
}

func naiveStripTags(s string) string {
	var sb strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			sb.WriteRune(' ')
			continue
		}
		if !inTag {
			sb.WriteRune(r)
		}
	}
	return strings.TrimSpace(sb.String())
}

func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "...[truncated]"
}

func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
