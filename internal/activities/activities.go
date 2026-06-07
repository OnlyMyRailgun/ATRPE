package activities

import (
	"context"

	"github.com/your-org/atrpe/internal/agents"
	"github.com/your-org/atrpe/internal/artifacts"
	"github.com/your-org/atrpe/internal/config"
	"github.com/your-org/atrpe/internal/knowledge"
	"github.com/your-org/atrpe/internal/objectstore"
)

// Activities bundles all Temporal activities with their dependencies.
type Activities struct {
	Config   *config.Settings
	Store    *knowledge.SQLiteStore
	Objects  objectstore.ObjectStore
	LLM      *agents.LLMClient
	Research *agents.ResearchAgent
	Design   *agents.DesignAgent
}

// New creates an Activities instance with all dependencies wired.
func New(cfg *config.Settings, store *knowledge.SQLiteStore, objects objectstore.ObjectStore) *Activities {
	llm := agents.NewLLMClient(agents.LLMConfig{
		Provider: cfg.LLMProvider,
		Model:    cfg.LLMModel,
		APIKey:   cfg.LLMAPIKey,
		BaseURL:  cfg.LLMBaseURL,
	})
	return &Activities{
		Config:   cfg,
		Store:    store,
		Objects:  objects,
		LLM:      llm,
		Research: agents.NewResearchAgent(llm),
		Design:   agents.NewDesignAgent(llm),
	}
}

// -- Discovery --

type DiscoverTopicsResult struct {
	Candidates []artifacts.TopicCandidate `json:"candidates"`
}

func (a *Activities) DiscoverTopics(ctx context.Context) (*DiscoverTopicsResult, error) {
	// Will be wired to topics.DiscoverAll in next iteration
	return &DiscoverTopicsResult{}, nil
}

// -- Research --

type ResearchInput struct {
	CandidateID string `json:"candidate_id"`
}

func (a *Activities) ResearchTopic(ctx context.Context, input ResearchInput) (*artifacts.TechnicalBrief, error) {
	candidate, err := a.Store.GetTopicCandidate(ctx, input.CandidateID)
	if err != nil {
		return nil, err
	}
	brief, err := a.Research.Run(ctx, candidate)
	if err != nil {
		return nil, err
	}
	// Save brief to object store + SQLite
	repo := artifacts.NewRepository(a.Store, a.Objects)
	if _, err := repo.SaveArtifact(ctx, "technical_briefs", brief.ArtifactID.String(), brief.TopicID, brief); err != nil {
		return nil, err
	}
	return &brief, nil
}

// -- Design --

type DesignInput struct {
	Brief artifacts.TechnicalBrief `json:"brief"`
}

func (a *Activities) DesignArchitecture(ctx context.Context, input DesignInput) (*artifacts.DesignArtifact, error) {
	design, err := a.Design.Run(ctx, input.Brief)
	if err != nil {
		return nil, err
	}
	repo := artifacts.NewRepository(a.Store, a.Objects)
	if _, err := repo.SaveArtifact(ctx, "design_artifacts", design.ArtifactID.String(), design.TopicID, design); err != nil {
		return nil, err
	}
	return &design, nil
}
