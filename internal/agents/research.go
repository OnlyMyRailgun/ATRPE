package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/your-org/atrpe/internal/artifacts"
)

// ResearchAgent synthesizes research into a TechnicalBrief.
type ResearchAgent struct {
	llm *LLMClient
}

// NewResearchAgent creates a research agent backed by an LLM.
func NewResearchAgent(llm *LLMClient) *ResearchAgent {
	return &ResearchAgent{llm: llm}
}

const researchSystemPrompt = `You are a technical research assistant. Given a topic, gather and synthesize information as if you had access to official documentation, RFCs, and high-quality articles.

Output a JSON object with this exact structure:
{
  "core_concepts": ["concept1", "concept2"],
  "supported_claims": ["claim1 with evidence", "claim2 with evidence"],
  "common_pitfalls": ["pitfall1", "pitfall2"],
  "research_questions": ["question1", "question2"],
  "success_criteria": ["criterion1", "criterion2"],
  "sources": [{"url": "https://...", "title": "Doc Title", "retrieved": "2024-01-01"}]
}

Be precise. Every claim must be verifiable. Every source must be real. Limit to 5 items per list.`

// Run executes the research agent and returns a TechnicalBrief.
func (a *ResearchAgent) Run(ctx context.Context, topic artifacts.TopicCandidate) (artifacts.TechnicalBrief, error) {
	userPrompt := fmt.Sprintf("Research this topic: %s\nURL: %s\nSource: %s", topic.Title, topic.URL, topic.Source)

	resp, err := a.llm.Chat(ctx, []ChatMessage{
		{Role: "system", Content: researchSystemPrompt},
		{Role: "user", Content: userPrompt},
	})
	if err != nil {
		return artifacts.TechnicalBrief{}, fmt.Errorf("research llm call: %w", err)
	}

	var result struct {
		CoreConcepts     []string              `json:"core_concepts"`
		SupportedClaims  []string              `json:"supported_claims"`
		CommonPitfalls   []string              `json:"common_pitfalls"`
		ResearchQuestions []string             `json:"research_questions"`
		SuccessCriteria  []string              `json:"success_criteria"`
		Sources          []artifacts.SourceRef `json:"sources"`
	}

	resp = extractJSON(resp)
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return artifacts.TechnicalBrief{}, fmt.Errorf("parse research output: %w (raw: %s)", err, truncate(resp, 200))
	}

	brief := artifacts.TechnicalBrief{
		BaseArtifact: artifacts.BaseArtifact{
			ArtifactID:   uuid.New(),
			ArtifactType: "technical_brief",
			Version:      1,
			TopicID:      topic.ID,
			CreatedAt:    time.Now().UTC(),
			Producer:     artifacts.AgentResearch,
		},
		CoreConcepts:     result.CoreConcepts,
		SupportedClaims:  result.SupportedClaims,
		CommonPitfalls:   result.CommonPitfalls,
		ResearchQuestions: result.ResearchQuestions,
		SuccessCriteria:  result.SuccessCriteria,
		Sources:          result.Sources,
	}

	return brief, nil
}
