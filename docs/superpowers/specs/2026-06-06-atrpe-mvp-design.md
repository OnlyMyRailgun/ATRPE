# ATRPE MVP Implementation-Oriented Design

## Purpose and Scope

ATRPE MVP is a Go-based system for producing high-quality Zenn technical articles from topic discovery through human-approved publishing.

The MVP must deliver an end-to-end workflow skeleton that a single developer can build in roughly four weeks. It must support cold-start topic discovery, human topic selection, research, design, local experiment validation, verification, article generation, publish approval, and publish recording.

The MVP explicitly does not include:

- Long-term learning-driven decision making.
- Retrospective 7d/30d/90d analysis workflows.
- Cloud experiment sandboxes.
- Qdrant or vector retrieval.
- Python as the main implementation language.

## Recommended Architecture

Use a `Go Workflow Core + ObjectStore Adapter Shell` architecture.

Temporal owns durable workflow orchestration. Go modules implement all system logic. External services are reached through narrow adapters so the MVP can start small and later swap implementations without rewriting the business workflow.

Core runtime components:

- `cmd/api`: GitHub webhook/API entrypoint.
- `cmd/worker`: Temporal worker process.
- `internal/workflows`: deterministic workflow definitions.
- `internal/activities`: Temporal activities for agent calls and external I/O.
- `internal/agents`: Research, Design, Experiment, Verification, and Writer implementations.
- `internal/artifacts`: artifact models and repository.
- `internal/objectstore`: local and Cloudflare R2 object storage adapters.
- `internal/knowledge`: SQLite knowledge store.
- `internal/github`: webhook validation and command parsing.
- `internal/topics`: cold-start topic discovery and scoring.
- `internal/config`: typed configuration from environment variables.

The `cmd/worker` binary must handle SIGTERM/SIGINT gracefully: stop accepting new activity tasks, allow in-flight activities a configurable grace period (default 30s), then shut down. This is a one-line Temporal worker option and costs nothing to include.

Use `log/slog` for all structured logging. Activities and workflows should accept a logger from context rather than using a global. Temporal's own logger can be bridged to slog for a single log stream.

The main workflow is `ArticleWorkflow`. Agents are Go interfaces called from Temporal activities. They are not microservices in the MVP.

## Object Storage

The MVP must not depend on AWS S3. Define an object storage interface and make Cloudflare R2 the deployed MVP default because it is S3-compatible and has a small free tier suitable for this scope.

Local development should use `LocalObjectStore` so tests and manual runs do not require cloud credentials.

```go
type URI string

type ObjectMetadata struct {
    ContentType string
    SizeBytes   int64
    ETag        string
}

type ObjectStore interface {
    Put(ctx context.Context, key string, body io.Reader, contentType string) (URI, error)
    Get(ctx context.Context, uri URI) (io.ReadCloser, error)
    Head(ctx context.Context, uri URI) (ObjectMetadata, error)
    Delete(ctx context.Context, uri URI) error
}
```

Required implementations:

- `LocalObjectStore`: writes under a configured local directory.
- `R2ObjectStore`: uses Cloudflare R2 through S3-compatible Go SDK configuration.

Future implementation:

- `OCIObjectStore`: optional adapter, not in MVP.

## Workflow State Machine

The article workflow states are:

- `DISCOVER`
- `WAIT_TOPIC_SELECTION`
- `RESEARCH`
- `DESIGN`
- `EXPERIMENT`
- `VERIFY`
- `GENERATE_ARTICLE`
- `WAIT_PUBLISH_APPROVAL`
- `PUBLISH`
- `PATCH_GENERATION`
- `DESIGN_UPDATE`
- `ESCALATED`
- `COMPLETED`
- `FAILED`
- `ABORTED`

Main path:

```text
DISCOVER
  -> WAIT_TOPIC_SELECTION
  -> RESEARCH
  -> DESIGN
  -> EXPERIMENT
  -> VERIFY
  -> GENERATE_ARTICLE
  -> WAIT_PUBLISH_APPROVAL
  -> PUBLISH
  -> COMPLETED
```

Verification failure path:

```text
VERIFY
  -> PATCH_GENERATION
  -> DESIGN_UPDATE
  -> EXPERIMENT
```

Publish approval wait path:

```text
WAIT_PUBLISH_APPROVAL
  -> PUBLISH on PublishApprovalSignal
  -> GENERATE_ARTICLE on RequestChangesSignal
  -> ABORTED on AbortSignal
```

When `RequestChangesSignal` is received during `WAIT_PUBLISH_APPROVAL`, the workflow stores `change_notes` and calls WriterAgent again. The WriterAgent receives the previous draft context plus `change_notes` and produces a new `ArticleDraft`. Larger redesign requests should be expressed through `/abort` plus a new workflow in the MVP; routing `/changes` back to `DESIGN` is deferred.

Escalated publish path:

```text
ESCALATED
  -> PUBLISH on RetrySignal when escalation reason is publish failure or manual PR merge confirmation
  -> ABORTED on AbortSignal
```

PublishActivity must be idempotent. If a PR for the article slug already exists, it must update or reuse that PR instead of creating a duplicate. If the PR has already been merged, PublishActivity records `PublishedArticle` and completes.

The remediation loop must run at most `MAX_REMEDIATION_ATTEMPTS`, default `3`. If remediation is exhausted, the workflow enters `ESCALATED`.

State categories:

- Active: `DISCOVER`, `RESEARCH`, `DESIGN`, `EXPERIMENT`, `VERIFY`, `GENERATE_ARTICLE`, `PATCH_GENERATION`, `DESIGN_UPDATE`, `PUBLISH`.
- Waiting: `WAIT_TOPIC_SELECTION`, `WAIT_PUBLISH_APPROVAL`.
- Recovery: `ESCALATED`.
- Terminal: `COMPLETED`, `FAILED`, `ABORTED`.

`RETRYING` is not a workflow state in the MVP. Temporal activity retry is configured at the activity level and surfaced through logs and workflow history, not as a durable business state.

Cloud approval is not part of the MVP main path. The feature flag `CLOUD_APPROVAL_ENABLED` defaults to `false`.

## Human Signals and GitHub Commands

The GitHub webhook layer validates requests, parses commands, and sends Temporal signals. It does not own article workflow state.

Command mapping:

| GitHub command | Signal | Applicable States | Purpose |
| --- | --- | --- | --- |
| `/select <candidate_id>` | `TopicSelectedSignal` | `WAIT_TOPIC_SELECTION` | Confirm a topic candidate by stable ID. |
| `/approve` | `PublishApprovalSignal` | `WAIT_PUBLISH_APPROVAL` | Approve publishing. |
| `/retry` | `RetrySignal` | `ESCALATED` | Retry the current recoverable step from escalation. |
| `/abort` | `AbortSignal` | `WAIT_PUBLISH_APPROVAL`, `ESCALATED` | Abort the workflow. |
| `/changes <notes>` | `RequestChangesSignal` | `WAIT_PUBLISH_APPROVAL` | Request changes with notes. |

Example topic selection payload:

```json
{ "candidate_id": "topic-123" }
```

The workflow must resolve `candidate_id` through `KnowledgeStore` before research starts. Topic titles are display text only and must not be used as workflow identity because titles can change and duplicate titles can exist.

Candidate ID generation must be deterministic and stable across discovery reruns:

```go
// candidate_id = sha256(source + "|" + url)[:12]
// Example: "github_trending|https://github.com/kubernetes/kubernetes" -> "a3f2c8e1b0d4"
func CandidateID(source, url string) string {
    h := sha256.Sum256([]byte(source + "|" + url))
    return hex.EncodeToString(h[:])[:12]
}
```

The ID is a 12-character hex string derived from the source name and source URL. This ensures that:
- The same external article always produces the same candidate ID.
- A `/select` command referring to a previous discovery list remains valid after reruns.
- Different sources can cover the same URL without collision (source is part of the hash input).

`/reject` is intentionally omitted from the MVP command set. Operators should use `/changes <notes>` for draft revisions or `/abort` for rejection.

## Internal Signal Fallback

GitHub webhook is the normal human signal entrypoint, but it must not be the only path. `cmd/api` must expose an internal-only fallback endpoint for operator recovery:

```http
POST /internal/workflows/{workflow_id}/signal
```

Request body:

```json
{
  "signal": "TopicSelectedSignal",
  "payload": { "candidate_id": "topic-123" }
}
```

This endpoint is not public. It should be bound to localhost or a private network in MVP deployments and protected with an operator token. It exists only to unblock workflows if GitHub webhook delivery, credentials, or network routing fail.

## Agent Contracts

All agents are Go interfaces with default in-process implementations.

```go
type ResearchAgent interface {
    Run(ctx context.Context, topic TopicCandidate) (TechnicalBrief, error)
}

type DesignAgent interface {
    Run(ctx context.Context, brief TechnicalBrief) (DesignArtifact, error)
    Update(ctx context.Context, design DesignArtifact, patch PatchResult) (DesignArtifact, error)
}

type ExperimentAgent interface {
    Run(ctx context.Context, design DesignArtifact) (ExperimentResult, error)
    Patch(ctx context.Context, design DesignArtifact, result ExperimentResult) (PatchResult, error)
}

type VerificationAgent interface {
    Run(ctx context.Context, brief TechnicalBrief, result ExperimentResult) (VerificationReport, error)
}

type WriterAgent interface {
    Run(ctx context.Context, brief TechnicalBrief, result ExperimentResult, report VerificationReport, changeNotes string) (ArticleDraft, error)
}

type CodeGenerator interface {
    GenerateGoModule(ctx context.Context, design DesignArtifact) (GeneratedModule, error)
}
```

### Research Agent

Input: `TopicCandidate`.

Output: `TechnicalBrief`.

Responsibilities:

- Gather official documentation, RFCs, and high-quality articles.
- Extract core concepts, supported claims, common pitfalls, and research questions.
- Produce success criteria for the article and experiment.

### Design Agent

Input: `TechnicalBrief`.

Output: `DesignArtifact`.

Responsibilities:

- Design example architecture and component interactions.
- Estimate experiment cost.
- Produce a test plan.
- Keep `RequiresCloudResources` false by default.
- Update the design after patch generation so code fixes and design intent remain aligned.

### Experiment Agent

Input: `DesignArtifact`.

Output: `ExperimentResult`.

MVP default language: Go.

Responsibilities:

- Generate a temporary Go module under a local workspace.
- Generate example code and tests through `CodeGenerator`.
- Run default Go validation commands: `go test ./...` and `go vet ./...`.
- Run `golangci-lint run` when `lint` is enabled.
- Run non-duplicate `TestPlan.TestCases[].Command` values after the default commands.
- Capture command stdout, stderr, exit code, duration, entrypoints, generated file paths, and raw status.
- Avoid deciding final pass/fail semantics beyond reporting whether commands executed and what they returned.

Experiment is the producer of executable evidence. Verification is the judge. This avoids double-running tests or splitting pass/fail semantics across two agents.

MVP code generation uses an external LLM API through an `LLMCodeGenerator` implementation. All LLM calls run inside Temporal activities, never inside deterministic workflow code.

The MVP default provider is **DeepSeek** (`deepseek-chat` model), chosen for its low cost and strong code generation performance. The provider is configured via environment variables, not hard-coded:

```
LLM_PROVIDER=deepseek
LLM_MODEL=deepseek-chat
LLM_API_KEY=<key>
LLM_BASE_URL=https://api.deepseek.com/v1
```

`LLMCodeGenerator` reads these settings and constructs an OpenAI-compatible HTTP client. DeepSeek's API is OpenAI-compatible, so switching to OpenAI or another compatible provider is a configuration change, not a code change. The generator prompt is constructed from `DesignArtifact` and must request a complete, buildable Go module including source files, `go.mod`, and tests.

Research, Design, Verification, and Writer agents also use LLM calls. They share the same provider configuration. Each agent constructs its own prompt from its input artifacts; prompt templates live alongside the agent implementations, not in configuration.

Python is not an MVP runner. The experiment layer must still expose an `ExperimentRunner` boundary so a Python runner can be added immediately after MVP if the article strategy shifts toward AI, MLOps, or data engineering content.

### Verification Agent

Input: `TechnicalBrief`, `ExperimentResult`.

Output: `VerificationReport`.

Checks:

- lint command result satisfies configured threshold.
- vet command result satisfies configured threshold.
- test command result satisfies configured threshold.
- cited links accessible through HTTP HEAD or fallback GET.

All required checks in `VERIFICATION_CHECKS` must pass for `PASS`. For MVP, every command-based check passes only when the corresponding command exits with code `0`; any non-zero exit code fails the check. If `lint` is configured and `golangci-lint` is missing or fails, lint is blocking. If lint is disabled, a missing linter is recorded as a warning.

The MVP does not include claim support ratio or citation completeness scoring.

### Writer Agent

Input: `TechnicalBrief`, `ExperimentResult`, `VerificationReport`, optional `changeNotes` string.

Output: `ArticleDraft`.

Required Zenn sections:

- Background.
- Architecture.
- Implementation.
- Evaluation.
- Troubleshooting.

The writer must produce Zenn metadata including emoji, type, topics, and `published: false` before approval.

## Artifact Models

All artifacts include a base envelope:

```go
type AgentName string

const (
    AgentResearch     AgentName = "research"
    AgentDesign       AgentName = "design"
    AgentExperiment   AgentName = "experiment"
    AgentVerification AgentName = "verification"
    AgentWriter       AgentName = "writer"
)

type BaseArtifact struct {
    ArtifactID        uuid.UUID `json:"artifact_id"`
    ArtifactType      string    `json:"artifact_type"`
    Version           int       `json:"version"`
    TopicID           string    `json:"topic_id"`
    CreatedAt         time.Time `json:"created_at"`
    Producer          AgentName `json:"producer"`
    ParentArtifactIDs []uuid.UUID `json:"parent_artifact_ids"`
}
```

Required artifact types:

- `TechnicalBrief`
- `DesignArtifact`
- `ExperimentResult`
- `VerificationReport`
- `PatchResult`
- `ArticleDraft`

Artifacts are serialized as JSON and stored in `ObjectStore`. SQLite may store artifact IDs, topic IDs, and object URIs for lookup, but object storage is the source for full artifact content.

`DesignArtifact` is the implementation contract for ExperimentAgent:

```go
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
```

`ExperimentResult` must preserve raw command evidence instead of only boolean summaries:

`GeneratedModule` is the output of `CodeGenerator.GenerateGoModule`:

```go
type GeneratedFile struct {
    Path    string `json:"path"`
    Content string `json:"content"`
}

type GeneratedModule struct {
    ModuleName string          `json:"module_name"`
    Files      []GeneratedFile `json:"files"`
    Entrypoint string          `json:"entrypoint"` // e.g. "cmd/example/main.go"
}
```

```go
type CommandResult struct {
    Name       string        `json:"name"`
    Args       []string      `json:"args"`
    ExitCode   int           `json:"exit_code"`
    Stdout     string        `json:"stdout"`
    Stderr     string        `json:"stderr"`
    DurationMS int64         `json:"duration_ms"`
}

type Environment struct {
    Type      string `json:"type"` // "local"
    Runtime   string `json:"runtime"` // "go"
    Workdir   string `json:"workdir"`
    Attempt   int    `json:"attempt"`
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
```

`VerificationReport` derives `lint_passed`, `vet_passed`, `tests_passed`, `links_passed`, and warnings from these command results.

```go
type VerificationReport struct {
    BaseArtifact
    LintPassed      bool            `json:"lint_passed"`
    VetPassed       bool            `json:"vet_passed"`
    TestsPassed     bool            `json:"tests_passed"`
    LinksPassed     bool            `json:"links_passed"`
    OverallPassed   bool            `json:"overall_passed"`
    BlockingIssues  []string        `json:"blocking_issues"`
    Warnings        []string        `json:"warnings"`
    CheckedCommands []CommandResult `json:"checked_commands"`
}
```

`PatchResult` is the remediation contract between ExperimentAgent and DesignAgent:

```go
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
```

## Knowledge System

SQLite is the MVP knowledge backend. The database path is `data/knowledge.db`.

Required tables:

```sql
CREATE TABLE topic_candidates (
    id TEXT PRIMARY KEY,
    source TEXT,
    title TEXT,
    url TEXT,
    score REAL,
    created_at TEXT
);

CREATE TABLE technical_briefs (
    id TEXT PRIMARY KEY,
    topic_id TEXT,
    artifact_uri TEXT,
    created_at TEXT
);

CREATE TABLE published_articles (
    id TEXT PRIMARY KEY,
    slug TEXT,
    title TEXT,
    published_at TEXT,
    platform TEXT,
    url TEXT,
    views INTEGER DEFAULT 0,
    likes INTEGER DEFAULT 0
);

CREATE TABLE citation_registry (
    id TEXT PRIMARY KEY,
    source_url TEXT,
    content_hash TEXT,
    hash_algorithm TEXT DEFAULT 'sha256',
    retrieved_at TEXT,
    UNIQUE(source_url, content_hash)
);

CREATE TABLE failed_patterns (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topic_id TEXT,
    error_stage TEXT,
    error_message TEXT,
    created_at TEXT
);

CREATE TABLE engagement_metrics (
    topic_id TEXT,
    platform TEXT,
    publish_date TEXT,
    views INTEGER,
    likes INTEGER
);
```

Recommended indexes:

```sql
CREATE INDEX idx_topic_candidates_created_at ON topic_candidates(created_at);
CREATE INDEX idx_technical_briefs_topic_id ON technical_briefs(topic_id);
CREATE INDEX idx_published_articles_slug ON published_articles(slug);
CREATE INDEX idx_citation_registry_source_url ON citation_registry(source_url);
CREATE INDEX idx_engagement_metrics_topic_date ON engagement_metrics(topic_id, publish_date);
```

KnowledgeStore contract:

```go
type KnowledgeStore interface {
    SaveTopicCandidate(ctx context.Context, candidate TopicCandidate) error
    GetTopicCandidate(ctx context.Context, candidateID string) (TopicCandidate, error)
    ListTopicCandidates(ctx context.Context, limit int) ([]TopicCandidate, error)
    SaveTechnicalBrief(ctx context.Context, brief TechnicalBrief, uri URI) error
    SavePublishedArticle(ctx context.Context, article PublishedArticle) error
    RegisterCitation(ctx context.Context, citation CitationRecord) error
    SaveFailedPattern(ctx context.Context, pattern FailedPattern) error
    SaveEngagementMetrics(ctx context.Context, metrics EngagementMetrics) error
}
```

### Artifact Storage Path Convention

Every artifact follows a consistent two-step storage pattern: content lives in ObjectStore, metadata lives in SQLite. The `artifacts/repository.go` layer performs both steps:

```
Artifact content → ObjectStore key: <artifact_type>/<artifact_id>.json
SQLite row        → <artifact_type>s table stores artifact_id, topic_id, artifact_uri, created_at
```

Reconstructing an artifact from its ID:

1. Query SQLite for the row with `artifact_id`, read `artifact_uri`.
2. Call `ObjectStore.Get(ctx, artifact_uri)` to retrieve the full JSON content.

Per-type key templates:

| Artifact Type | ObjectStore Key | SQLite Table |
| --- | --- | --- |
| `TechnicalBrief` | `technical_briefs/<uuid>.json` | `technical_briefs` |
| `DesignArtifact` | `design_artifacts/<uuid>.json` | `design_artifacts` |
| `ExperimentResult` | `experiment_results/<uuid>.json` | `experiment_results` |
| `VerificationReport` | `verification_reports/<uuid>.json` | `verification_reports` |
| `PatchResult` | `patch_results/<uuid>.json` | `patch_results` |
| `ArticleDraft` | `article_drafts/<uuid>.json` | `article_drafts` |

SQLite schema for the artifact metadata tables follows the same shape as `technical_briefs` (see above), with `id TEXT PRIMARY KEY`, `topic_id TEXT`, `artifact_uri TEXT`, and `created_at TEXT` columns. The `topic_candidates` table is an exception — it stores candidate data inline rather than referencing ObjectStore URIs, because candidates are discovery output that predates artifact generation.

### SQLite Disaster Recovery

Use **Litestream** (MIT) for continuous SQLite WAL replication to R2. It runs as a sidecar process alongside the worker and requires zero application code:

```bash
litestream replicate data/knowledge.db s3://${R2_BUCKET}/knowledge.db \
  --endpoint ${R2_ENDPOINT} \
  --access-key-id ${R2_ACCESS_KEY_ID} \
  --secret-access-key ${R2_SECRET_ACCESS_KEY}
```

This is an operational addition for deployed environments, not a code dependency. Local development does not require Litestream.

## Cold Start Topic Discovery

Cold start is enabled by default. It must not depend on learning output or engagement metrics.

Sources:

- GitHub trending.
- Hacker News.
- Zenn trending.
- Qiita trending.
- RSS feeds.

Default fixed scoring:

```yaml
novelty: 0.4
practicality: 0.4
timing: 0.2
```

Initial normalized score rules:

```go
novelty = 1.0 - math.Min(float64(japaneseArticleCount)/50.0, 1.0)
practicality = math.Min(float64(githubStars)/10000.0, 1.0)
timing = recencyScore(publishedAt)
```

`recencyScore` returns `1.0` for items within 7 days, `0.5` for items within 30 days, `0.2` for items within 90 days, and `0.0` for older or undated items. If a source does not expose GitHub stars, use source-specific popularity normalized to `0..1`; if no popularity signal exists, use `0.5` as the MVP neutral default.

The discovery activity stores candidates in SQLite and returns the top candidates for human selection. Zenn and Qiita sources are used for competition and gap detection in the Japanese technical writing context, not for copying article content.

## Experiment Workspace Lifecycle

ExperimentAgent writes generated code into a local workspace that is unique per topic and remediation attempt:

```go
type ExperimentWorkspace struct {
    RootDir   string    `json:"root_dir"`
    TopicID   string    `json:"topic_id"`
    Attempt   int       `json:"attempt"`
    CreatedAt time.Time `json:"created_at"`
}
```

Cleanup policy:

```yaml
cleanup_policy:
  on_success:
    retain_for_debugging_hours: 24
  on_failure:
    retain_for_debugging_hours: 72
  on_abort:
    delete_immediately: true
```

Each remediation attempt creates a fresh workspace. Reusing a previous failed workspace is not allowed in MVP because it makes results harder to reproduce. The workspace path and generated file list must be recorded in `ExperimentResult`.

## Temporal Search Attributes

The MVP should define these search attributes even if operational dashboards are minimal at first:

- `topic_id`
- `workflow_state`
- `article_slug`

Workflow activities must update `workflow_state` as the workflow advances. `topic_id` is set after `TopicSelectedSignal` is resolved. `article_slug` is set after article draft generation.

## Workflow Versioning

During the MVP build, in-flight workflows may be terminated and recreated when the workflow definition changes. The MVP does not require `workflow.GetVersion()` guards until workflows are expected to survive code upgrades in production.

Before the first production run, any state transition change must either:

- terminate and recreate all in-flight MVP workflows, or
- introduce Temporal versioning through `workflow.GetVersion()`.

## Publish Activity

WriterAgent generates Zenn markdown with `published: false`. PublishActivity owns the final publish mutation:

1. Read `ArticleDraft` from `ObjectStore`.
2. Parse frontmatter and set `published: true`.
3. Write the final markdown artifact through `ObjectStore`.
4. Create or update a branch in the configured Zenn GitHub repository.
5. Commit the final markdown.
6. Open a pull request via GitHub API.
7. Merge the pull request if auto-merge is configured.
8. Record `PublishedArticle` in SQLite with `slug`, `title`, `platform`, `url`, `views=0`, and `likes=0` only after merge/publish succeeds.

Git operations (clone, branch, commit, push) use **go-git** (`github.com/go-git/go-git/v5`), a pure-Go Git implementation. This avoids depending on a system `git` binary, keeps the Docker image minimal, and eliminates shell-out risks. PR creation and merge use the GitHub REST API via `net/http` or `google/go-github`.

If repository auto-merge is not configured, PublishActivity records the PR URL as a blocking issue and the workflow enters `ESCALATED` instead of inserting a `published_articles` row. The operator can merge the PR manually and send `/retry`, or abort the workflow. The workflow must not silently mark a PR-only draft as a published article.

## Configuration

Use a typed Go settings struct loaded from environment variables.

```go
type Settings struct {
    CloudApprovalEnabled      bool
    ExperimentEnvType         string
    DefaultExperimentLanguage string
    VerificationChecks        []string
    MaxRemediationAttempts    int
    ColdStartEnabled          bool
    TopicSources              []string
    MaxActiveWorkflows        int
    MaxParallelExperiments    int
    MaxMonthlyArticles        int
    TemporalHostPort          string
    TemporalNamespace         string
    TemporalTaskQueue         string
    InternalSignalToken       string
    ArtifactStoreType         string
    LocalArtifactDir          string
    R2Bucket                  string
    R2Endpoint                string
    R2AccessKeyID             string
    R2SecretAccessKey         string
    LLMProvider               string
    LLMModel                  string
    LLMAPIKey                 string
    LLMBaseURL                string
}
```

Defaults:

- `CLOUD_APPROVAL_ENABLED=false`
- `EXPERIMENT_ENV_TYPE=local`
- `DEFAULT_EXPERIMENT_LANGUAGE=go`
- `VERIFICATION_CHECKS=lint,vet,tests,links`
- `MAX_REMEDIATION_ATTEMPTS=3`
- `COLD_START_ENABLED=true`
- `TOPIC_SOURCES=github_trending,hackernews,zenn_trending,qiita_trending,rss_feeds`
- `MAX_ACTIVE_WORKFLOWS=3`
- `MAX_PARALLEL_EXPERIMENTS=2`
- `MAX_MONTHLY_ARTICLES=8`
- `TEMPORAL_HOST_PORT=localhost:7233`
- `TEMPORAL_NAMESPACE=default`
- `TEMPORAL_TASK_QUEUE=atrpe-workflow-queue`

For local development with multiple contributors, set `TEMPORAL_TASK_QUEUE` to a developer-specific value (e.g., `atrpe-workflow-queue-alice`) so workers don't steal each other's tasks.
- `LLM_PROVIDER=deepseek`
- `LLM_MODEL=deepseek-chat`
- `LLM_API_KEY` must be set before running any agent activity.
- `LLM_BASE_URL=https://api.deepseek.com/v1`
- `INTERNAL_SIGNAL_TOKEN` must be set before enabling the internal fallback endpoint outside local development.
- `ARTIFACT_STORE_TYPE=local` for development.

## Proposed Directory Structure

```text
atrpe/
├── cmd/
│   ├── api/
│   │   └── main.go
│   └── worker/
│       └── main.go
├── internal/
│   ├── activities/
│   ├── agents/
│   │   ├── research.go
│   │   ├── design.go
│   │   ├── experiment.go
│   │   ├── verification.go
│   │   └── writer.go
│   ├── artifacts/
│   │   ├── models.go
│   │   └── repository.go
│   ├── config/
│   │   └── settings.go
│   ├── github/
│   │   ├── webhook.go
│   │   └── commands.go
│   ├── knowledge/
│   │   ├── schema.sql
│   │   └── sqlite_store.go
│   ├── objectstore/
│   │   ├── local.go
│   │   └── r2.go
│   ├── topics/
│   └── workflows/
│       └── article_workflow.go
├── data/
│   └── .gitkeep
└── docs/
```

## Testing Strategy

Unit tests:

- GitHub command parser.
- Internal signal fallback request validation.
- Workflow transition helpers.
- Artifact serialization and validation.
- SQLite knowledge store.
- Local object store.
- Cold-start scoring.

Activity tests:

- Research activity with mocked sources.
- Design activity with deterministic fixture input.
- Go experiment runner using a temporary module.
- Experiment workspace cleanup policy.
- Verification link checker with local HTTP test server.
- Writer activity metadata generation.
- Publish activity frontmatter mutation from `published: false` to `published: true`.

End-to-end local test:

- Start workflow in a Temporal test environment or local Temporal server.
- Simulate topic discovery.
- Send `TopicSelectedSignal`.
- Resolve the selected `candidate_id` through `KnowledgeStore`.
- Run research, design, experiment, verification, and writer activities.
- Send `PublishApprovalSignal`.
- Assert article draft exists, artifact URIs exist, and knowledge records were written.
- Assert `/changes <notes>` during `WAIT_PUBLISH_APPROVAL` regenerates the article draft.
- Assert the internal signal fallback can send the same signal payload as GitHub command parsing.

Acceptance criteria:

- A cold-start topic list can be generated.
- `/select` advances a workflow from waiting to research.
- `/select` uses `candidate_id`, not topic title, as the stable workflow input.
- Go example code can be generated and command results can be collected for `go test` and `go vet`.
- Verification passes only when configured lint/static checks, tests, and links pass.
- Verification failure enters remediation and stops after three attempts.
- `PatchResult`, `DesignArtifact`, and `VerificationReport` are fully defined and serializable.
- Experiment workspaces are retained or deleted according to cleanup policy.
- A Zenn markdown draft with metadata is generated before publish approval.
- Publish activity flips Zenn frontmatter to `published: true` before creating a PR or final publish artifact.
- GitHub webhook failure can be bypassed through the internal signal endpoint.
- Temporal search attributes are set for `topic_id`, `workflow_state`, and `article_slug`.
- Artifact content is stored through `ObjectStore`.
- SQLite records topic candidates, technical briefs, citations, failures, published articles, and engagement metrics.

## Four-Week MVP Milestones

Week 1:

- Initialize Go module and project structure.
- Define config, artifact models, object store interfaces, and SQLite schema.
- Implement local object store and SQLite store.
- Implement workflow state types and command parser.
- Implement internal signal fallback endpoint.

Week 2:

- Implement Temporal worker and `ArticleWorkflow` happy path.
- Implement Research and Design agent interfaces with fixture-backed or LLM-backed MVP implementations.
- Persist artifacts and topic candidates.

Week 3:

- Implement Go Experiment Agent.
- Implement Verification Agent.
- Implement experiment workspace cleanup policy.
- Implement remediation loop: `PATCH_GENERATION -> DESIGN_UPDATE -> EXPERIMENT`.
- Implement Writer Agent draft generation.

Week 4:

- Implement GitHub webhook signal mapping.
- Implement PublishActivity frontmatter update and Zenn repository PR creation.
- Add Cloudflare R2 object store adapter.
- Run local end-to-end workflow.
- Add minimal deployment configuration.
- Produce the first manually approved Zenn draft.

## Evolution to Full Version

The Full version should evolve by replacing adapters and agent implementations while preserving workflow contracts:

| Module | MVP | Full |
| --- | --- | --- |
| Experiment Sandbox | Local Go validation | Docker isolation and runtime metrics |
| Verification | lint, static checks, tests, links | claim verification and citation reasoning |
| Knowledge | SQLite | Qdrant plus vector retrieval |
| Learning | none; metrics tables only | feedback loop that updates topic weights |
| Cloud Approval | feature flag disabled | GitHub Issue approval workflow |
| Retrospective | none | 7d/30d/90d workflows |
| Experiment Languages | Go runner only | Python runner for AI, MLOps, and data engineering articles |

## Key Design Decisions

- Go is the system implementation language.
- Go is also the default experiment article language for MVP.
- Python is not part of the MVP runtime or experiment runner, but the runner boundary must support adding it as the first post-MVP language extension.
- Cloudflare R2 is the default cloud object store adapter.
- Local object storage remains the default development mode.
- Temporal workflow code stays deterministic; all external I/O lives in activities.
- Learning is not implemented in MVP; engagement metrics tables are retained for future ingestion and retrospective workflows.
- Verification failure patches code first, then updates design, then reruns experiment.
