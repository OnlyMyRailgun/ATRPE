package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/OnlyMyRailgun/ATRPE/internal/artifacts"
	"github.com/OnlyMyRailgun/ATRPE/internal/research"
)

// ResearchAgent synthesizes research into a TechnicalBrief.
type ResearchAgent struct {
	llm       *LLMClient
	fetcher   research.WebFetcher

	// Optional: if set, raw HTML snapshots are persisted.
	snapshots SnapshotStore
	// Optional: if set, citations are registered in the knowledge store.
	citations CitationStore
}

// SetSnapshotStore configures source snapshot persistence (ObjectStore).
func (a *ResearchAgent) SetSnapshotStore(store SnapshotStore) { a.snapshots = store }

// SetCitationStore configures citation registration (SQLite knowledge store).
func (a *ResearchAgent) SetCitationStore(store CitationStore) { a.citations = store }

// NewResearchAgent creates a research agent backed by an LLM and web fetcher.
func NewResearchAgent(llm *LLMClient) *ResearchAgent {
	return &ResearchAgent{llm: llm, fetcher: research.NewWebFetcher()}
}

const urlDiscoveryPrompt = `You are a technical research assistant. Given a topic to research, suggest 5-7 URLs that would contain authoritative, up-to-date information about this topic.

Prioritize:
1. Official documentation or GitHub README (most important)
2. RFCs or design documents
3. High-quality technical blog posts or tutorials
4. Published benchmarks or case studies

Output a JSON object:
{
  "search_queries": ["specific search query 1", "query 2"],
  "suggested_urls": [
    {"url": "https://...", "title": "Page Title", "reason": "Why this source is relevant"}
  ]
}

Limit to 7 URLs. Prefer official sources over blog posts. Include the topic's primary URL.`

const synthesisPrompt = `You are a skeptical technical researcher. Below are the ACTUAL fetched contents of documentation and articles about a topic. Synthesize them into a TechnicalBrief.

## Fetched Sources
%s

## Instructions
- Extract the core concepts FROM THE PROVIDED CONTENT. Do not invent.
- Every claim must include a source index like "[source #1]" referencing which fetched source it comes from.
- If sources conflict, note the conflict and cite both.
- Mark confidence per claim: [CERTAIN] | [LIKELY] | [NEEDS VERIFICATION].
- If a source doesn't cover a concept, don't make it up.

Output a JSON object:
{
  "core_concepts": [
    {"text": "concept description", "source_index": 1, "confidence": "CERTAIN"}
  ],
  "supported_claims": [
    {"text": "claim with evidence", "source_index": 2, "confidence": "LIKELY"}
  ],
  "common_pitfalls": [
    {"text": "pitfall description", "source_index": 3, "confidence": "CERTAIN"}
  ],
  "research_questions": ["question based on gaps in coverage", "..."],
  "success_criteria": ["criterion1", "..."]
}

Limit to 5 items per list.`

// Run executes the research agent with two-phase web-backed research.
// Phase A: LLM suggests URLs → Phase B: fetch URLs → synthesize with real content.
// Returns a TechnicalBrief whose Sources list only contains URLs that were actually fetched.
func (a *ResearchAgent) Run(ctx context.Context, topic artifacts.TopicCandidate) (artifacts.TechnicalBrief, error) {
	// Phase A: URL discovery via LLM
	userPrompt := fmt.Sprintf("Research this topic:\nTitle: %s\nURL: %s\nSource: %s", topic.Title, topic.URL, topic.Source)

	resp, err := a.llm.ChatWithMaxTokens(ctx, []ChatMessage{
		{Role: "system", Content: todayPrefix() + " " + urlDiscoveryPrompt},
		{Role: "user", Content: userPrompt},
	}, 2048)
	if err != nil {
		return artifacts.TechnicalBrief{}, fmt.Errorf("research url discovery: %w", err)
	}

	var urlResult struct {
		SearchQueries []string `json:"search_queries"`
		SuggestedURLs []struct {
			URL    string `json:"url"`
			Title  string `json:"title"`
			Reason string `json:"reason"`
		} `json:"suggested_urls"`
	}

	resp = extractJSON(resp)
	json.Unmarshal([]byte(resp), &urlResult)

	// Build URL list: start with the topic URL, then add suggested URLs
	urlsToFetch := []string{topic.URL}
	for _, s := range urlResult.SuggestedURLs {
		if s.URL != "" && s.URL != topic.URL {
			urlsToFetch = append(urlsToFetch, s.URL)
		}
	}

	// Phase B: Fetch and synthesize
	var sourceText strings.Builder
	var sourceRefs []artifacts.SourceRef
	fetchedCount := 0

	if a.fetcher != nil && len(urlsToFetch) > 0 {
		pages, fetchErr := a.fetcher.FetchMultiple(ctx, urlsToFetch, 3)
		if fetchErr == nil {
			for _, page := range pages {
				if page.Error != "" || page.Content == "" {
					continue
				}
				fetchedCount++
				sourceIndex := fetchedCount // 1-based for LLM prompts
				fmt.Fprintf(&sourceText, "\n### Source #%d: %s (%s)\n%s\n",
					sourceIndex, page.Title, page.URL, page.Content)

				ref := artifacts.SourceRef{
					URL:         page.URL,
					Title:       page.Title,
					Retrieved:   page.RetrievedAt.Format("2006-01-02"),
					ContentHash: page.ContentHash,
					StatusCode:  page.StatusCode,
					Fetched:     true,
				}

				// Persist raw HTML snapshot to ObjectStore
				if a.snapshots != nil {
					snapKey := SourcesSnapshotKey(page.ContentHash)
					if err := a.snapshots.Put(ctx, snapKey,
						strings.NewReader(page.Content),
						"text/plain; charset=utf-8"); err != nil {
						fmt.Printf("⚠️ failed to save snapshot for %s: %v\n", page.URL, err)
					} else {
						ref.SnapshotURI = snapKey
					}
				}

				// Register citation in knowledge store
				if a.citations != nil {
					if err := a.citations.RegisterCitation(ctx,
						page.URL,
						page.ContentHash,
						page.RetrievedAt.Format("2006-01-02"),
					); err != nil {
						fmt.Printf("⚠️ failed to register citation for %s: %v\n", page.URL, err)
					}
				}

				sourceRefs = append(sourceRefs, ref)
			}
		}
	}

	// If we couldn't fetch anything, fall back to LLM-only with disclaimer
	if fetchedCount == 0 {
		brief, err := a.fallbackResearch(ctx, topic)
		// Add unfetched URL as reference anyway, marked Fetched=false
		if len(urlsToFetch) > 0 {
			brief.Sources = append(brief.Sources, artifacts.SourceRef{
				URL:     topic.URL,
				Title:   topic.Title,
				Fetched: false,
			})
		}
		return brief, err
	}

	// Synthesize from real sources
	prompt := fmt.Sprintf(synthesisPrompt, sourceText.String())
	resp, err = a.llm.ChatWithMaxTokens(ctx, []ChatMessage{
		{Role: "user", Content: prompt},
	}, 8192)
	if err != nil {
		return artifacts.TechnicalBrief{}, fmt.Errorf("research synthesis: %w", err)
	}

	var result struct {
		CoreConcepts      []structuredClaim `json:"core_concepts"`
		SupportedClaims   []structuredClaim `json:"supported_claims"`
		CommonPitfalls    []structuredClaim `json:"common_pitfalls"`
		ResearchQuestions []string          `json:"research_questions"`
		SuccessCriteria   []string          `json:"success_criteria"`
	}

	resp = extractJSON(resp)
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return artifacts.TechnicalBrief{}, fmt.Errorf("parse research output: %w (raw: %s)", err, truncate(resp, 200))
	}

	// Convert structured claims back to annotated text for backward compat
	brief := artifacts.TechnicalBrief{
		BaseArtifact: artifacts.BaseArtifact{
			ArtifactID:   uuid.New(),
			ArtifactType: "technical_brief",
			Version:      1,
			TopicID:      topic.ID,
			CreatedAt:    time.Now().UTC(),
			Producer:     artifacts.AgentResearch,
		},
		CoreConcepts:      formatStructuredClaims(result.CoreConcepts, sourceRefs),
		SupportedClaims:   formatStructuredClaims(result.SupportedClaims, sourceRefs),
		CommonPitfalls:    formatStructuredClaims(result.CommonPitfalls, sourceRefs),
		ResearchQuestions: result.ResearchQuestions,
		SuccessCriteria:   result.SuccessCriteria,
		Sources:           sourceRefs,
	}

	return brief, nil
}

type structuredClaim struct {
	Text        string `json:"text"`
	SourceIndex int    `json:"source_index"`
	Confidence  string `json:"confidence"`
}

// formatStructuredClaims merges structured {text, source_index, confidence} items
// into human-readable strings like "text [CERTAIN — source #1: https://...]".
func formatStructuredClaims(items []structuredClaim, sources []artifacts.SourceRef) []string {
	out := make([]string, len(items))
	for i, item := range items {
		s := item.Text
		sourceTag := ""
		if item.SourceIndex > 0 && item.SourceIndex <= len(sources) {
			sourceTag = fmt.Sprintf("source #%d: %s", item.SourceIndex, sources[item.SourceIndex-1].URL)
		}
		confTag := item.Confidence
		if confTag == "" {
			confTag = "NEEDS VERIFICATION"
		}
		out[i] = fmt.Sprintf("%s [%s — %s]", s, confTag, sourceTag)
	}
	return out
}

// fallbackResearch is used when web fetching fails — LLM-only with warning.
func (a *ResearchAgent) fallbackResearch(ctx context.Context, topic artifacts.TopicCandidate) (artifacts.TechnicalBrief, error) {
	userPrompt := fmt.Sprintf(
		"Research this topic: %s\nURL: %s\nSource: %s\n\n⚠️ CRITICAL: Web fetching FAILED. Use ONLY your training knowledge. "+
			"Mark EVERY claim as [NEEDS VERIFICATION — web fetch failed, training data only]. "+
			"Do NOT invent URLs or claim to have fetched sources.",
		topic.Title, topic.URL, topic.Source)

	resp, err := a.llm.Chat(ctx, []ChatMessage{
		{Role: "system", Content: todayPrefix() + " " + synthesisPrompt},
		{Role: "user", Content: userPrompt},
	})
	if err != nil {
		return artifacts.TechnicalBrief{}, fmt.Errorf("fallback research: %w", err)
	}

	var result struct {
		CoreConcepts     []string `json:"core_concepts"`
		SupportedClaims  []string `json:"supported_claims"`
		CommonPitfalls   []string `json:"common_pitfalls"`
		ResearchQuestions []string `json:"research_questions"`
		SuccessCriteria  []string `json:"success_criteria"`
	}

	resp = extractJSON(resp)
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return artifacts.TechnicalBrief{}, fmt.Errorf("parse fallback research: %w", err)
	}

	return artifacts.TechnicalBrief{
		BaseArtifact: artifacts.BaseArtifact{
			ArtifactID:   uuid.New(),
			ArtifactType: "technical_brief",
			Version:      1,
			TopicID:      topic.ID,
			CreatedAt:    time.Now().UTC(),
			Producer:     artifacts.AgentResearch,
		},
		CoreConcepts:      result.CoreConcepts,
		SupportedClaims:   result.SupportedClaims,
		CommonPitfalls:    result.CommonPitfalls,
		ResearchQuestions: result.ResearchQuestions,
		SuccessCriteria:   result.SuccessCriteria,
		Sources: []artifacts.SourceRef{{
			URL:     topic.URL,
			Title:   topic.Title,
			Fetched: false,
		}},
	}, nil
}
