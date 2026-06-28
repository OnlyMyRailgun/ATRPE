package activities

import (
	"bytes"
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

// AuditAction is the recommendation action after content audit.
type AuditAction string

const (
	AuditKeep     AuditAction = "keep"     // write immediately — low saturation, high differentiation
	AuditPass     AuditAction = "pass"     // writeable but not urgent
	AuditDeepDive AuditAction = "deep_dive" // needs more research before writing
	AuditUpdate   AuditAction = "update"   // update an existing article instead
	AuditSkip     AuditAction = "skip"     // don't write — saturated or undifferentiable
)

// ContentAuditResult is the per-candidate audit produced by the Content Audit stage.
type ContentAuditResult struct {
	CandidateID     string     `json:"candidate_id"`
	Passes          bool       `json:"passes"`
	Action          AuditAction `json:"action"`          // keep/pass/deep_dive/update/skip
	FailReason      string     `json:"fail_reason,omitempty"`
	Recommendation  float64    `json:"recommendation"`   // 0..1
	WhyNow          string     `json:"why_now"`          // why worth writing today
	SaturationLevel string     `json:"saturation_level"` // "low"|"medium"|"high"|"saturated"
	Differentiation string     `json:"differentiation"`  // what makes this article unique
	ExistingGaps    string     `json:"existing_gaps"`    // what's missing in existing coverage
	TestablePart    string     `json:"testable_part"`    // the code-verifiable component
	Risks           string     `json:"risks"`            // what could go wrong
	SuggestedTitle  string     `json:"suggested_title"`  // proposed article title
	DontWriteReason string     `json:"dont_write_reason"` // reason NOT to write (or empty)
	SimilarityScore float64    `json:"similarity_score"`  // 0..1 overlap with existing articles
	ExistingCount   int        `json:"existing_count"`   // approx existing articles
	OwnOverlap      bool       `json:"own_overlap"`      // true if similar to own history
	AuditURI        string     `json:"audit_uri"`         // ObjectStore key of persisted audit scan
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
// Raw scan results are persisted to ObjectStore for auditability.
func (a *Activities) AuditTopics(ctx context.Context, input AuditTopicsInput) (*AuditTopicsResult, error) {
	var audits []ContentAuditResult

	for _, c := range input.Candidates {
		searchKeyword := extractKeyword(c.Title)

		// Search across platforms
		zennResults := searchZenn(ctx, searchKeyword)
		qiitaResults := searchQiita(ctx, searchKeyword)
		hnResults := searchHN(ctx, searchKeyword)
		ownHistory := checkOwnHistory(ctx, a.Store, searchKeyword)

		searches := []platformSearch{zennResults, qiitaResults, hnResults, ownHistory}

		// Persist raw scan to ObjectStore for audit trail
		auditURI := ""
		if a.Objects != nil {
			scanData := map[string]interface{}{
				"candidate_id": c.ID,
				"keyword":      searchKeyword,
				"timestamp":    time.Now().UTC().Format(time.RFC3339),
				"searches":     searches,
			}
			scanJSON, _ := json.Marshal(scanData)
			key := fmt.Sprintf("content_audits/%s/%s.json", c.ID, time.Now().UTC().Format("2006-01-02"))
			_, err := a.Objects.Put(ctx, key, bytes.NewReader(scanJSON), "application/json")
			if err == nil {
				auditURI = key
			}
		}

		audit, err := a.runAuditForCandidate(ctx, c, searchKeyword, searches)
		if err != nil {
			// Degrade gracefully
			audits = append(audits, ContentAuditResult{
				CandidateID: c.ID, Action: AuditPass, Passes: true,
				Recommendation: 0.5, SaturationLevel: "unknown",
				AuditURI: auditURI, SuggestedTitle: c.Title,
			})
			continue
		}
		audit.CandidateID = c.ID
		audit.AuditURI = auditURI

		// Compute Jaccard-like similarity using existing article titles
		audit.SimilarityScore = computeSimilarity(c.Title, searches)

		// Map passes to action if LLM didn't set one
		if audit.Action == "" {
			audit.Action = decideAction(audit)
		}

		audits = append(audits, audit)
	}

	return &AuditTopicsResult{Audits: audits}, nil
}

// computeSimilarity calculates a rough 0..1 overlap score between the candidate
// title and existing articles found on each platform.
func computeSimilarity(candidateTitle string, searches []platformSearch) float64 {
	totalArticles := 0
	totalOverlap := 0.0

	keywords := tokenize(candidateTitle)
	if len(keywords) == 0 {
		return 0.0
	}

	for _, s := range searches {
		for _, title := range s.TopTitles {
			titleKeywords := tokenize(title)
			intersection := intersectCount(keywords, titleKeywords)
			union := len(keywords) + len(titleKeywords) - intersection
			if union > 0 {
				totalOverlap += float64(intersection) / float64(union)
			}
			totalArticles++
		}
	}
	if totalArticles == 0 {
		return 0.0
	}
	return totalOverlap / float64(totalArticles)
}

func tokenize(s string) []string {
	var tokens []string
	seen := make(map[string]bool)
	for _, t := range strings.Fields(strings.ToLower(s)) {
		t = strings.Trim(t, ".,;:'\"!?()[]{}-")
		if len(t) > 2 && !seen[t] {
			seen[t] = true
			tokens = append(tokens, t)
		}
	}
	return tokens
}

func intersectCount(a, b []string) int {
	bSet := make(map[string]bool, len(b))
	for _, v := range b {
		bSet[v] = true
	}
	count := 0
	for _, v := range a {
		if bSet[v] {
			count++
		}
	}
	return count
}

// decideAction maps audit fields to a recommendation action.
func decideAction(r ContentAuditResult) AuditAction {
	if !r.Passes {
		return AuditSkip
	}
	switch r.SaturationLevel {
	case "low":
		if r.Recommendation >= 0.7 {
			return AuditKeep
		}
		return AuditPass
	case "medium":
		return AuditPass
	case "high":
		return AuditDeepDive
	case "saturated":
		return AuditSkip
	default:
		if r.OwnOverlap {
			return AuditUpdate
		}
		return AuditPass
	}
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

## Action Decision
- "keep" — write immediately: low saturation, high differentiation, testable
- "pass" — writeable but not urgent: medium saturation or timing is weak
- "deep_dive" — needs more research before writing: promising but complex
- "update" — update an existing article instead: we have related published content
- "skip" — don't write: saturated or undifferentiable

## Output Format
{
  "passes": true,
  "action": "keep",
  "fail_reason": "",
  "recommendation": 0.85,
  "similarity_score": 0.15,
  "why_now": "...",
  "saturation_level": "low",
  "differentiation": "...",
  "existing_gaps": "...",
  "testable_part": "...",
  "risks": "...",
  "suggested_title": "...",
  "dont_write_reason": ""
}

If you recommend skipping, set passes=false, action="skip", fail_reason with WHY.`

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
	var parsed struct {
		Passes          bool    `json:"passes"`
		Action          string  `json:"action"`
		FailReason      string  `json:"fail_reason"`
		Recommendation  float64 `json:"recommendation"`
		SimilarityScore float64 `json:"similarity_score"`
		WhyNow          string  `json:"why_now"`
		SaturationLevel string  `json:"saturation_level"`
		Differentiation string  `json:"differentiation"`
		ExistingGaps    string  `json:"existing_gaps"`
		TestablePart    string  `json:"testable_part"`
		Risks           string  `json:"risks"`
		SuggestedTitle  string  `json:"suggested_title"`
		DontWriteReason string  `json:"dont_write_reason"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		return ContentAuditResult{}, fmt.Errorf("parse audit output: %w", err)
	}

	audit := ContentAuditResult{
		Passes:          parsed.Passes,
		Action:          AuditAction(parsed.Action),
		FailReason:      parsed.FailReason,
		Recommendation:  parsed.Recommendation,
		WhyNow:          parsed.WhyNow,
		SaturationLevel: parsed.SaturationLevel,
		Differentiation: parsed.Differentiation,
		ExistingGaps:    parsed.ExistingGaps,
		TestablePart:    parsed.TestablePart,
		Risks:           parsed.Risks,
		SuggestedTitle:  parsed.SuggestedTitle,
		DontWriteReason: parsed.DontWriteReason,
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
