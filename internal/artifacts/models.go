package artifacts

import (
	"time"

	"github.com/google/uuid"
	"github.com/OnlyMyRailgun/ATRPE/internal/objectstore"
)

// URI is an alias for objectstore.URI for convenience.
type URI = objectstore.URI

// AgentName identifies which agent produced an artifact.
type AgentName string

const (
	AgentResearch     AgentName = "research"
	AgentDesign       AgentName = "design"
	AgentExperiment   AgentName = "experiment"
	AgentVerification AgentName = "verification"
	AgentWriter       AgentName = "writer"
)

// BaseArtifact is embedded in all artifact types.
type BaseArtifact struct {
	ArtifactID        uuid.UUID   `json:"artifact_id"`
	ArtifactType      string      `json:"artifact_type"`
	Version           int         `json:"version"`
	TopicID           string      `json:"topic_id"`
	CreatedAt         time.Time   `json:"created_at"`
	Producer          AgentName   `json:"producer"`
	ParentArtifactIDs []uuid.UUID `json:"parent_artifact_ids"`
}

// -- Design Artifact --

type Component struct {
	Name       string `json:"name"`
	Type       string `json:"type"` // "service" | "queue" | "db" | "external" | "library"
	Technology string `json:"technology"`
}

type Interaction struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Protocol string `json:"protocol"` // "http" | "grpc" | "event" | "file" | "call"
}

type TestCase struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Command     string `json:"command"`
	Expected    string `json:"expected"`
}

type TestPlan struct {
	Strategy  string     `json:"strategy"`
	TestCases []TestCase `json:"test_cases"`
}

type DesignArtifact struct {
	BaseArtifact
	Components             []Component   `json:"components"`
	Interactions           []Interaction `json:"interactions"`
	Assumptions            []string      `json:"assumptions"`
	Constraints            []string      `json:"constraints"`
	SuccessCriteria        []string      `json:"success_criteria"`
	TestPlan               TestPlan      `json:"test_plan"`
	EstimatedCostUSD       float64       `json:"estimated_cost_usd"`
	RequiresCloudResources bool          `json:"requires_cloud_resources"`
	DiagramURI             URI           `json:"diagram_uri,omitempty"`
}

// -- Experiment Result --

type GeneratedFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type GeneratedModule struct {
	ModuleName string          `json:"module_name"`
	Files      []GeneratedFile `json:"files"`
	Entrypoint string          `json:"entrypoint"`
}

type CommandResult struct {
	Name       string   `json:"name"`
	Args       []string `json:"args"`
	ExitCode   int      `json:"exit_code"`
	Stdout     string   `json:"stdout"`
	Stderr     string   `json:"stderr"`
	DurationMS int64    `json:"duration_ms"`
}

type Environment struct {
	Type    string `json:"type"`    // "local"
	Runtime string `json:"runtime"` // "go"
	Workdir string `json:"workdir"`
	Attempt int    `json:"attempt"`
}

type ExperimentResult struct {
	BaseArtifact
	ExecutionID         string          `json:"execution_id"`
	Environment         Environment     `json:"environment"`
	ExperimentLanguage  string          `json:"experiment_language"`
	SourceRepositoryURI string          `json:"source_repository_uri"`
	CommitSHA           string          `json:"commit_sha"`
	Entrypoints         []string        `json:"entrypoints"`
	GeneratedFiles      []string        `json:"generated_files"`
	Commands            []CommandResult `json:"commands"`
}

// -- Verification Report --

type VerificationReport struct {
	BaseArtifact
	LintPassed      bool            `json:"lint_passed"`
	VetPassed       bool            `json:"vet_passed"`
	TestsPassed     bool            `json:"tests_passed"`
	LinksPassed     bool            `json:"links_passed"`
	CitationsPassed bool            `json:"citations_passed"`
	ClaimsMatched   int             `json:"claims_matched"`
	ClaimsUnmatched int             `json:"claims_unmatched"`
	OverallPassed   bool            `json:"overall_passed"`
	BlockingIssues  []string        `json:"blocking_issues"`
	Warnings        []string        `json:"warnings"`
	CheckedCommands []CommandResult `json:"checked_commands"`
}

// -- Patch Result --

type PatchedFile struct {
	Path    string `json:"path"`
	OldHash string `json:"old_hash"`
	NewHash string `json:"new_hash"`
}

type PatchResult struct {
	BaseArtifact
	OriginalArtifactID uuid.UUID       `json:"original_artifact_id"`
	PatchedFiles       []PatchedFile   `json:"patched_files"`
	FailedCommands     []CommandResult `json:"failed_commands"`
	RemediationReason  string          `json:"remediation_reason"`
}

// -- Topic & Brief --

type TopicCandidate struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Score     float64   `json:"score"`
	CreatedAt time.Time `json:"created_at"`
}

// ArticleAngle is a refined topic suggestion with specific writing angles.
type ArticleAngle struct {
	CandidateID string   `json:"candidate_id"`
	ProjectName string   `json:"project_name"`
	ProjectURL  string   `json:"project_url"`
	Angles      []string `json:"angles"` // 1–3 specific article ideas
}

type TechnicalBrief struct {
	BaseArtifact
	CoreConcepts     []string    `json:"core_concepts"`
	SupportedClaims  []string    `json:"supported_claims"`
	CommonPitfalls   []string    `json:"common_pitfalls"`
	ResearchQuestions []string   `json:"research_questions"`
	SuccessCriteria  []string    `json:"success_criteria"`
	Sources          []SourceRef `json:"sources"`
}

type SourceRef struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Retrieved   string `json:"retrieved"`
	ContentHash string `json:"content_hash"` // sha256[:16] of fetched content
	SnapshotURI string `json:"snapshot_uri"` // ObjectStore key of raw HTML
	StatusCode  int    `json:"status_code"`  // HTTP status when fetched
	Fetched     bool   `json:"fetched"`      // true if actually fetched (vs fallback)
}

// -- Article Draft --

type ArticleDraft struct {
	BaseArtifact
	Slug      string          `json:"slug"`
	Title     string          `json:"title"`
	Emoji     string          `json:"emoji"`
	Type      string          `json:"type"` // "tech" | "idea"
	Topics    []string        `json:"topics"`
	Published bool            `json:"published"`
	Sections  ArticleSections `json:"sections"`
	Body      string          `json:"body"` // full markdown
}

type ArticleSections struct {
	Background      string `json:"background"`
	Architecture    string `json:"architecture"`
	Implementation  string `json:"implementation"`
	Evaluation      string `json:"evaluation"`
	Troubleshooting string `json:"troubleshooting"`
}

// -- Knowledge System Models --

type PublishedArticle struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	PublishedAt time.Time `json:"published_at"`
	Platform    string    `json:"platform"`
	URL         string    `json:"url"`
	Views       int       `json:"views"`
	Likes       int       `json:"likes"`
}

type CitationRecord struct {
	ID            string `json:"id"`
	SourceURL     string `json:"source_url"`
	ContentHash   string `json:"content_hash"`
	HashAlgorithm string `json:"hash_algorithm"`
	RetrievedAt   string `json:"retrieved_at"`
}

type FailedPattern struct {
	ID           int    `json:"id"`
	TopicID      string `json:"topic_id"`
	ErrorStage   string `json:"error_stage"`
	ErrorMessage string `json:"error_message"`
	CreatedAt    string `json:"created_at"`
}

type EngagementMetrics struct {
	TopicID     string `json:"topic_id"`
	Platform    string `json:"platform"`
	PublishDate string `json:"publish_date"`
	Views       int    `json:"views"`
	Likes       int    `json:"likes"`
}

type ExperimentWorkspace struct {
	RootDir   string    `json:"root_dir"`
	TopicID   string    `json:"topic_id"`
	Attempt   int       `json:"attempt"`
	CreatedAt time.Time `json:"created_at"`
}
