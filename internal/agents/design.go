package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/your-org/atrpe/internal/artifacts"
)

// DesignAgent produces a DesignArtifact from a TechnicalBrief.
type DesignAgent struct {
	llm *LLMClient
}

// NewDesignAgent creates a design agent backed by an LLM.
func NewDesignAgent(llm *LLMClient) *DesignAgent {
	return &DesignAgent{llm: llm}
}

const designSystemPrompt = `You are a software design assistant. Given a technical brief, design an example architecture that demonstrates the key concepts.

Output a JSON object with this exact structure:
{
  "components": [{"name": "...", "type": "service|queue|db|external|library", "technology": "Go"}],
  "interactions": [{"from": "comp-a", "to": "comp-b", "protocol": "http|grpc|event|file|call"}],
  "assumptions": ["..."],
  "constraints": ["..."],
  "success_criteria": ["..."],
  "test_plan": {
    "strategy": "unit tests with go test",
    "test_cases": [
      {"name": "...", "description": "...", "command": "go test ./...", "expected": "exit code 0"}
    ]
  },
  "estimated_cost_usd": 0,
  "requires_cloud_resources": false
}

Keep it small (2-4 components). The design must be implementable in a single Go module. No cloud services. TestPlan commands must be valid shell commands.`

// Run produces a DesignArtifact from a technical brief.
func (a *DesignAgent) Run(ctx context.Context, brief artifacts.TechnicalBrief) (artifacts.DesignArtifact, error) {
	briefJSON, _ := json.Marshal(brief)
	userPrompt := fmt.Sprintf("Design an example architecture for this technical brief:\n%s", string(briefJSON))

	resp, err := a.llm.Chat(ctx, []ChatMessage{
		{Role: "system", Content: designSystemPrompt},
		{Role: "user", Content: userPrompt},
	})
	if err != nil {
		return artifacts.DesignArtifact{}, fmt.Errorf("design llm call: %w", err)
	}

	resp = extractJSON(resp)

	var result struct {
		Components             []artifacts.Component   `json:"components"`
		Interactions           []artifacts.Interaction `json:"interactions"`
		Assumptions            []string                `json:"assumptions"`
		Constraints            []string                `json:"constraints"`
		SuccessCriteria        []string                `json:"success_criteria"`
		TestPlan               artifacts.TestPlan      `json:"test_plan"`
		EstimatedCostUSD       float64                 `json:"estimated_cost_usd"`
		RequiresCloudResources bool                    `json:"requires_cloud_resources"`
	}

	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return artifacts.DesignArtifact{}, fmt.Errorf("parse design output: %w", err)
	}

	return artifacts.DesignArtifact{
		BaseArtifact: artifacts.BaseArtifact{
			ArtifactID:        uuid.New(),
			ArtifactType:      "design_artifact",
			Version:           1,
			TopicID:           brief.TopicID,
			CreatedAt:         time.Now().UTC(),
			Producer:          artifacts.AgentDesign,
			ParentArtifactIDs: []uuid.UUID{brief.ArtifactID},
		},
		Components:             result.Components,
		Interactions:           result.Interactions,
		Assumptions:            result.Assumptions,
		Constraints:            result.Constraints,
		SuccessCriteria:        result.SuccessCriteria,
		TestPlan:               result.TestPlan,
		EstimatedCostUSD:       result.EstimatedCostUSD,
		RequiresCloudResources: result.RequiresCloudResources,
	}, nil
}

// Update refines a design after a patch has been applied to fix code issues.
func (a *DesignAgent) Update(ctx context.Context, design artifacts.DesignArtifact, patch artifacts.PatchResult) (artifacts.DesignArtifact, error) {
	designJSON, _ := json.Marshal(design)
	patchJSON, _ := json.Marshal(patch)
	userPrompt := fmt.Sprintf("Original design:\n%s\n\nPatch result (code changes applied):\n%s\n\nUpdate the design to reflect the code fixes.", string(designJSON), string(patchJSON))

	resp, err := a.llm.Chat(ctx, []ChatMessage{
		{Role: "system", Content: designSystemPrompt},
		{Role: "user", Content: userPrompt},
	})
	if err != nil {
		return artifacts.DesignArtifact{}, fmt.Errorf("design update llm call: %w", err)
	}

	resp = extractJSON(resp)

	var result struct {
		Components             []artifacts.Component   `json:"components"`
		Interactions           []artifacts.Interaction `json:"interactions"`
		Assumptions            []string                `json:"assumptions"`
		Constraints            []string                `json:"constraints"`
		SuccessCriteria        []string                `json:"success_criteria"`
		TestPlan               artifacts.TestPlan      `json:"test_plan"`
		EstimatedCostUSD       float64                 `json:"estimated_cost_usd"`
		RequiresCloudResources bool                    `json:"requires_cloud_resources"`
	}

	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return artifacts.DesignArtifact{}, fmt.Errorf("parse design update output: %w", err)
	}

	return artifacts.DesignArtifact{
		BaseArtifact: artifacts.BaseArtifact{
			ArtifactID:        uuid.New(),
			ArtifactType:      "design_artifact",
			Version:           design.Version + 1,
			TopicID:           design.TopicID,
			CreatedAt:         time.Now().UTC(),
			Producer:          artifacts.AgentDesign,
			ParentArtifactIDs: []uuid.UUID{design.ArtifactID, patch.ArtifactID},
		},
		Components:             result.Components,
		Interactions:           result.Interactions,
		Assumptions:            result.Assumptions,
		Constraints:            result.Constraints,
		SuccessCriteria:        result.SuccessCriteria,
		TestPlan:               result.TestPlan,
		EstimatedCostUSD:       result.EstimatedCostUSD,
		RequiresCloudResources: result.RequiresCloudResources,
	}, nil
}
