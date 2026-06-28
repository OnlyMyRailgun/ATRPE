package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/OnlyMyRailgun/ATRPE/internal/agents"
	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
	"github.com/OnlyMyRailgun/ATRPE/internal/knowledge"
)

// ContentAuditResult is the per-candidate audit produced by the Content Audit stage.
type ContentAuditResult struct {
	CandidateID     string  `json:"candidate_id"`
	Passes          bool    `json:"passes"`
	FailReason      string  `json:"fail_reason,omitempty"`
	Recommendation  float64 `json:"recommendation"`   // 0..1
	WhyNow          string  `json:"why_now"`          // why worth writing today
	SaturationLevel string  `json:"saturation_level"` // "low"|"medium"|"high"|"saturated"
	Differentiation string  `json:"differentiation"`  // what makes this article unique
	ExistingGaps    string  `json:"existing_gaps"`    // what's missing in existing coverage
	TestablePart    string  `json:"testable_part"`    // the code-verifiable component
	Risks           string  `json:"risks"`            // what could go wrong
	SuggestedTitle  string  `json:"suggested_title"`  // proposed article title
	DontWriteReason string  `json:"dont_write_reason"` // reason NOT to write (or empty)
	ExistingCount   int     `json:"existing_count"`   // approx existing articles
	OwnOverlap      bool    `json:"own_overlap"`      // true if similar to own history
}

// AuditTopicsInput carries the candidates into the audit activity.
type AuditTopicsInput struct {
	Candidates []artifacts.TopicCandidate `json:"candidates"`
}

// AuditTopicsResult holds the audit results for all candidates.
type AuditTopicsResult struct {
	Audits []ContentAuditResult `json:"audits"`
}

// searchResult is a lightweight summary from one platform search.
type platformSearch struct {
	Platform string `json:"platform"`
	Query    string `json:"query"`
	Count    int    `json:"count"`
	TopTitles []string `json:"top_titles"`
	Error    string `json:"error,omitempty"`
}

// AuditTopics searches Zenn, Qiita, GitHub, HN, RSS, and own history for each candidate,
// then calls the LLM to produce a structured content audit per candidate.
func (a *Activities) AuditTopics(ctx context.Context, input AuditTopicsInput) (*AuditTopicsResult, error) {
	var audits []ContentAuditResult

	for _, c := range input.Candidates {
		searchKeyword := extractKeyword(c.Title)

		// Search across platforms
		zennResults := searchZenn(ctx, searchKeyword)
		qiitaResults := searchQiita(ctx, searchKeyword)
		hnResults := searchHN(ctx, searchKeyword)
		ownHistory := checkOwnHistory(ctx, a.Store, searchKeyword)

		// Build the audit prompt with real search data
		audit, err := a.runAuditForCandidate(ctx, c, searchKeyword, []platformSearch{
			zennResults, qiitaResults, hnResults, ownHistory,
		})
		if err != nil {
			// Degrade gracefully — mark as passes with a warning
			audits = append(audits, ContentAuditResult{
				CandidateID:    c.ID,
				Passes:         true,
				FailReason:     "",
				Recommendation: 0.5,
				WhyNow:         "Audit unavailable — fallback pass",
				SaturationLevel: "unknown",
				Differentiation: "",
				ExistingGaps:    "",
				TestablePart:    "",
				Risks:           "Content audit failed to complete",
				SuggestedTitle:  c.Title,
			})
			continue
		}
		audit.CandidateID = c.ID
		audits = append(audits, audit)
	}

	return &AuditTopicsResult{Audits: audits}, nil
}

// extractKeyword takes a repo name like "owner/project-name" and returns "project name".
func extractKeyword(title string) string {
	parts := strings.Split(title, "/")
	keyword := parts[len(parts)-1]
	keyword = strings.ReplaceAll(keyword, "-", " ")
	keyword = strings.ReplaceAll(keyword, "_", " ")
	return strings.TrimSpace(keyword)
}

func searchZenn(ctx context.Context, keyword string) platformSearch {
	result := platformSearch{Platform: "zenn", Query: keyword}
	query := url.QueryEscape(keyword)
	apiURL := fmt.Sprintf("https://zenn.dev/api/articles?q=%s&order=latest&count=5", query)

	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()

	var apiResp struct {
		Articles []struct {
			Title string `json:"title"`
			Slug  string `json:"slug"`
		} `json:"articles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		result.Error = err.Error()
		return result
	}

	result.Count = len(apiResp.Articles)
	for _, a := range apiResp.Articles {
		result.TopTitles = append(result.TopTitles, a.Title)
		if len(result.TopTitles) >= 5 {
			break
		}
	}
	return result
}

func searchQiita(ctx context.Context, keyword string) platformSearch {
	result := platformSearch{Platform: "qiita", Query: keyword}
	query := url.QueryEscape(keyword)
	apiURL := fmt.Sprintf("https://qiita.com/api/v2/items?query=%s&per_page=5", query)

	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()

	var items []struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		result.Error = err.Error()
		return result
	}

	result.Count = len(items)
	for _, item := range items {
		result.TopTitles = append(result.TopTitles, item.Title)
		if len(result.TopTitles) >= 5 {
			break
		}
	}
	return result
}

func searchHN(ctx context.Context, keyword string) platformSearch {
	result := platformSearch{Platform: "hackernews", Query: keyword}
	query := url.QueryEscape(keyword)
	apiURL := fmt.Sprintf("https://hn.algolia.com/api/v1/search?query=%s&hitsPerPage=5", query)

	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()

	var apiResp struct {
		Hits []struct {
			Title string `json:"title"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		result.Error = err.Error()
		return result
	}

	result.Count = len(apiResp.Hits)
	for _, h := range apiResp.Hits {
		result.TopTitles = append(result.TopTitles, h.Title)
		if len(result.TopTitles) >= 5 {
			break
		}
	}
	return result
}

// checkOwnHistory searches the local knowledge store for overlapping topics.
func checkOwnHistory(ctx context.Context, store *knowledge.SQLiteStore, keyword string) platformSearch {
	result := platformSearch{Platform: "atrpe_history", Query: keyword}

	// Get recent topic candidates
	candidates, err := store.ListTopicCandidates(ctx, 50)
	if err == nil {
		lowerKW := strings.ToLower(keyword)
		for _, c := range candidates {
			if strings.Contains(strings.ToLower(c.Title), lowerKW) {
				result.Count++
				result.TopTitles = append(result.TopTitles, c.Title)
				if len(result.TopTitles) >= 5 {
					break
				}
			}
		}
	}
	return result
}

const auditSystemPrompt = `You are a technical editor for Zenn (Japanese developer platform). Evaluate whether a topic is worth writing about RIGHT NOW.

## Evaluation Criteria
1. **Saturation**: How many existing articles cover this? (Zenn, Qiita, HN, our own history)
   - "low" = 0-2 existing articles → GREEN FLAG
   - "medium" = 3-7 existing → YELLOW FLAG (need strong differentiation)
   - "high" = 8-15 existing → RED FLAG (only if unique angle)
   - "saturated" = 16+ existing → FAIL (don't write)
2. **Differentiation**: What unique angle can we offer that existing articles don't?
3. **Testability**: Can we write COMPILABLE, RUNNABLE code for this? (A must for ATRPE)
4. **Timeliness**: Why is this worth reading TODAY? (recent release, trending, underserved niche)
5. **Risk**: What could make this article fail? (complexity, lack of sources, too broad)

## Output Format
Output a single JSON object:
{
  "passes": true,
  "fail_reason": "",
  "recommendation": 0.85,
  "why_now": "Kubernetes 1.31 just released with new Gateway API features...",
  "saturation_level": "low",
  "differentiation": "Unlike existing articles that explain the API spec, we actually build and test a custom gateway controller",
  "existing_gaps": "Existing articles are all high-level summaries. None include running code or actual benchmarks.",
  "testable_part": "Write a Go gateway controller, test it with kind cluster, benchmark latency",
  "risks": "Gateway API is still evolving; article may age. Mitigation: target v1.1 stable.",
  "suggested_title": "Goで作るKubernetes Gateway Controller — コードから学ぶGateway API v1.1",
  "dont_write_reason": ""
}

If you recommend NOT writing, set passes=false, fail_reason with WHY, and dont_write_reason with the key argument against.`

func (a *Activities) runAuditForCandidate(ctx context.Context, c artifacts.TopicCandidate, keyword string, searches []platformSearch) (ContentAuditResult, error) {
	// Build the audit input
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Topic: %s\nURL: %s\nSource: %s\n\n", c.Title, c.URL, c.Source))
	sb.WriteString("Platform searches for existing coverage:\n\n")

	for _, s := range searches {
		sb.WriteString(fmt.Sprintf("### %s\n", s.Platform))
		if s.Error != "" {
			sb.WriteString(fmt.Sprintf("  Search error: %s\n", s.Error))
		} else {
			sb.WriteString(fmt.Sprintf("  Found: %d articles\n", s.Count))
			for _, t := range s.TopTitles {
				sb.WriteString(fmt.Sprintf("  - %s\n", t))
			}
		}
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("\nToday's date: %s\n", time.Now().Format("2006-01-02")))

	// Call LLM
	resp, err := a.LLM.ChatWithMaxTokens(ctx, []agents.ChatMessage{
		{Role: "system", Content: auditSystemPrompt},
		{Role: "user", Content: sb.String()},
	}, 2048)
	if err != nil {
		return ContentAuditResult{}, fmt.Errorf("audit llm call: %w", err)
	}

	resp = agents.ExtractJSON(resp)
	var audit ContentAuditResult
	if err := json.Unmarshal([]byte(resp), &audit); err != nil {
		return ContentAuditResult{}, fmt.Errorf("parse audit output: %w", err)
	}

	// Count total existing articles across platforms
	for _, s := range searches {
		audit.ExistingCount += s.Count
	}
	audit.OwnOverlap = hasOwnOverlap(searches)

	return audit, nil
}

func hasOwnOverlap(searches []platformSearch) bool {
	for _, s := range searches {
		if s.Platform == "atrpe_history" && s.Count > 0 {
			return true
		}
	}
	return false
}

// EmojiForTopic returns an emoji based on the recommendation score.
func (a ContentAuditResult) EmojiForTopic() string {
	switch {
	case a.Recommendation >= 0.8:
		return "🔥"
	case a.Recommendation >= 0.6:
		return "✅"
	case a.Recommendation >= 0.4:
		return "🤔"
	default:
		return "👀"
	}
}

func shorten(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
