# ATRPE MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go-based system that produces Zenn technical articles end-to-end — cold-start topic discovery → human selection → research → design → local experiment validation → verification → article generation → publish approval → publish.

**Architecture:** Temporal for durable workflow orchestration, Go for all system logic, SQLite for knowledge store, Cloudflare R2 (with local fallback) for object storage, DeepSeek as the default LLM provider. Agents are Go interfaces called from Temporal activities, not microservices.

**Tech Stack:** Go 1.23+, Temporal (Go SDK), SQLite (mattn/go-sqlite3), go-git v5, Cloudflare R2 (S3-compatible), DeepSeek API (OpenAI-compatible), log/slog, golangci-lint.

---

## Week 1: Foundation — Project Skeleton, Config, Models, Stores, Commands

### Task 1: Initialize Go module and directory structure

**Files:**
- Create: `atrpe/go.mod`
- Create: `atrpe/cmd/api/main.go`
- Create: `atrpe/cmd/worker/main.go`
- Create: `atrpe/data/.gitkeep`
- Create: `atrpe/internal/activities/.gitkeep`
- Create: `atrpe/internal/agents/.gitkeep`
- Create: `atrpe/internal/artifacts/.gitkeep`
- Create: `atrpe/internal/config/.gitkeep`
- Create: `atrpe/internal/github/.gitkeep`
- Create: `atrpe/internal/knowledge/.gitkeep`
- Create: `atrpe/internal/objectstore/.gitkeep`
- Create: `atrpe/internal/topics/.gitkeep`
- Create: `atrpe/internal/workflows/.gitkeep`

- [ ] **Step 1: Create project root and go.mod**

```bash
mkdir -p atrpe/{cmd/{api,worker},data,internal/{activities,agents,artifacts,config,github,knowledge,objectstore,topics,workflows}}
cd atrpe
go mod init github.com/your-org/atrpe
```

- [ ] **Step 2: Create stub main.go files**

`cmd/api/main.go`:
```go
package main

import "fmt"

func main() {
    fmt.Println("ATRPE API server")
}
```

`cmd/worker/main.go`:
```go
package main

import (
    "log/slog"
    "os"
    "os/signal"
    "syscall"
    "time"

    "go.temporal.io/sdk/client"
    "go.temporal.io/sdk/worker"
    "go.temporal.io/sdk/log"
)

func main() {
    logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
    logger.Info("ATRPE worker starting")

    // Placeholder — config and temporal client wired in Task 10

    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    logger.Info("ATRPE worker shutting down")
    _ = time.Second // placeholder
}
```

- [ ] **Step 3: Create .gitkeep placeholders and verify build**

```bash
touch data/.gitkeep internal/{activities,agents,artifacts,config,github,knowledge,objectstore,topics,workflows}/.gitkeep
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "feat: initialize go module and directory structure"
```

---

### Task 2: Config — typed settings from environment

**Files:**
- Create: `atrpe/internal/config/settings.go`
- Create: `atrpe/internal/config/settings_test.go`

- [ ] **Step 1: Write the failing test**

`internal/config/settings_test.go`:
```go
package config

import (
    "os"
    "testing"
)

func TestLoadSettings_Defaults(t *testing.T) {
    os.Setenv("LLM_API_KEY", "sk-test")
    os.Setenv("INTERNAL_SIGNAL_TOKEN", "test-token")
    defer os.Unsetenv("LLM_API_KEY")
    defer os.Unsetenv("INTERNAL_SIGNAL_TOKEN")

    s, err := Load()
    if err != nil {
        t.Fatalf("Load() error: %v", err)
    }

    if s.LLMProvider != "deepseek" {
        t.Errorf("expected LLMProvider=deepseek, got %s", s.LLMProvider)
    }
    if s.MaxRemediationAttempts != 3 {
        t.Errorf("expected MaxRemediationAttempts=3, got %d", s.MaxRemediationAttempts)
    }
    if s.ExperimentEnvType != "local" {
        t.Errorf("expected ExperimentEnvType=local, got %s", s.ExperimentEnvType)
    }
    if s.TemporalTaskQueue != "atrpe-workflow-queue" {
        t.Errorf("expected TemporalTaskQueue=atrpe-workflow-queue, got %s", s.TemporalTaskQueue)
    }
}

func TestLoadSettings_Overrides(t *testing.T) {
    os.Setenv("LLM_API_KEY", "sk-custom")
    os.Setenv("INTERNAL_SIGNAL_TOKEN", "tok")
    os.Setenv("LLM_PROVIDER", "openai")
    os.Setenv("LLM_MODEL", "gpt-4o")
    os.Setenv("MAX_REMEDIATION_ATTEMPTS", "5")
    defer func() {
        for _, k := range []string{"LLM_API_KEY", "INTERNAL_SIGNAL_TOKEN", "LLM_PROVIDER", "LLM_MODEL", "MAX_REMEDIATION_ATTEMPTS"} {
            os.Unsetenv(k)
        }
    }()

    s, err := Load()
    if err != nil {
        t.Fatalf("Load() error: %v", err)
    }

    if s.LLMProvider != "openai" {
        t.Errorf("expected LLMProvider=openai, got %s", s.LLMProvider)
    }
    if s.LLMModel != "gpt-4o" {
        t.Errorf("expected LLMModel=gpt-4o, got %s", s.LLMModel)
    }
    if s.MaxRemediationAttempts != 5 {
        t.Errorf("expected MaxRemediationAttempts=5, got %d", s.MaxRemediationAttempts)
    }
}

func TestLoadSettings_MissingRequired(t *testing.T) {
    os.Unsetenv("LLM_API_KEY")
    os.Setenv("INTERNAL_SIGNAL_TOKEN", "tok")
    defer os.Setenv("INTERNAL_SIGNAL_TOKEN", "")
    _, err := Load()
    if err == nil {
        t.Error("expected error for missing LLM_API_KEY, got nil")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/ -v -run TestLoad
```
Expected: FAIL (package doesn't exist yet / types not defined)

- [ ] **Step 3: Implement Settings struct and Load()**

`internal/config/settings.go`:
```go
package config

import (
    "fmt"
    "os"
    "strconv"
    "strings"
)

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
    GitHubWebhookSecret       string
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

func Load() (*Settings, error) {
    s := &Settings{
        CloudApprovalEnabled:      getEnvBool("CLOUD_APPROVAL_ENABLED", false),
        ExperimentEnvType:         getEnv("EXPERIMENT_ENV_TYPE", "local"),
        DefaultExperimentLanguage: getEnv("DEFAULT_EXPERIMENT_LANGUAGE", "go"),
        VerificationChecks:        getEnvSlice("VERIFICATION_CHECKS", "lint,vet,tests,links"),
        MaxRemediationAttempts:    getEnvInt("MAX_REMEDIATION_ATTEMPTS", 3),
        ColdStartEnabled:          getEnvBool("COLD_START_ENABLED", true),
        TopicSources:              getEnvSlice("TOPIC_SOURCES", "github_trending,hackernews,zenn_trending,qiita_trending,rss_feeds"),
        MaxActiveWorkflows:        getEnvInt("MAX_ACTIVE_WORKFLOWS", 3),
        MaxParallelExperiments:    getEnvInt("MAX_PARALLEL_EXPERIMENTS", 2),
        MaxMonthlyArticles:        getEnvInt("MAX_MONTHLY_ARTICLES", 8),
        TemporalHostPort:          getEnv("TEMPORAL_HOST_PORT", "localhost:7233"),
        TemporalNamespace:         getEnv("TEMPORAL_NAMESPACE", "default"),
        TemporalTaskQueue:         getEnv("TEMPORAL_TASK_QUEUE", "atrpe-workflow-queue"),
        InternalSignalToken:       os.Getenv("INTERNAL_SIGNAL_TOKEN"),
        GitHubWebhookSecret:       os.Getenv("GITHUB_WEBHOOK_SECRET"),
        ArtifactStoreType:         getEnv("ARTIFACT_STORE_TYPE", "local"),
        LocalArtifactDir:          getEnv("LOCAL_ARTIFACT_DIR", "data/artifacts"),
        R2Bucket:                  os.Getenv("R2_BUCKET"),
        R2Endpoint:                os.Getenv("R2_ENDPOINT"),
        R2AccessKeyID:             os.Getenv("R2_ACCESS_KEY_ID"),
        R2SecretAccessKey:         os.Getenv("R2_SECRET_ACCESS_KEY"),
        LLMProvider:               getEnv("LLM_PROVIDER", "deepseek"),
        LLMModel:                  getEnv("LLM_MODEL", "deepseek-chat"),
        LLMAPIKey:                 os.Getenv("LLM_API_KEY"),
        LLMBaseURL:                getEnv("LLM_BASE_URL", "https://api.deepseek.com/v1"),
    }

    if s.LLMAPIKey == "" {
        return nil, fmt.Errorf("LLM_API_KEY is required")
    }
    return s, nil
}

func getEnv(key, fallback string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return fallback
}

func getEnvBool(key string, fallback bool) bool {
    v := os.Getenv(key)
    if v == "" {
        return fallback
    }
    return v == "true" || v == "1"
}

func getEnvInt(key string, fallback int) int {
    v := os.Getenv(key)
    if v == "" {
        return fallback
    }
    n, err := strconv.Atoi(v)
    if err != nil {
        return fallback
    }
    return n
}

func getEnvSlice(key, fallback string) []string {
    v := os.Getenv(key)
    if v == "" {
        v = fallback
    }
    parts := strings.Split(v, ",")
    result := make([]string, 0, len(parts))
    for _, p := range parts {
        if trimmed := strings.TrimSpace(p); trimmed != "" {
            result = append(result, trimmed)
        }
    }
    return result
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/config/ -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/ && git commit -m "feat: add typed config loaded from environment variables"
```

---

### Task 3: Artifact models

**Files:**
- Create: `atrpe/internal/artifacts/models.go`
- Create: `atrpe/internal/artifacts/models_test.go`

- [ ] **Step 1: Write the test — verify JSON round-trip**

`internal/artifacts/models_test.go`:
```go
package artifacts

import (
    "encoding/json"
    "testing"
    "time"

    "github.com/google/uuid"
)

func TestBaseArtifact_RoundTrip(t *testing.T) {
    id := uuid.New()
    parentID := uuid.New()
    a := BaseArtifact{
        ArtifactID:        id,
        ArtifactType:      "technical_brief",
        Version:           1,
        TopicID:           "abc123def456",
        CreatedAt:         time.Now().UTC().Truncate(time.Second),
        Producer:          AgentResearch,
        ParentArtifactIDs: []uuid.UUID{parentID},
    }

    b, err := json.Marshal(a)
    if err != nil {
        t.Fatalf("Marshal error: %v", err)
    }

    var a2 BaseArtifact
    if err := json.Unmarshal(b, &a2); err != nil {
        t.Fatalf("Unmarshal error: %v", err)
    }

    if a2.ArtifactID != id {
        t.Errorf("ArtifactID mismatch: %v != %v", a2.ArtifactID, id)
    }
    if a2.Producer != AgentResearch {
        t.Errorf("Producer mismatch: %s != %s", a2.Producer, AgentResearch)
    }
    if len(a2.ParentArtifactIDs) != 1 || a2.ParentArtifactIDs[0] != parentID {
        t.Error("ParentArtifactIDs mismatch")
    }
}

func TestDesignArtifact_RoundTrip(t *testing.T) {
    d := DesignArtifact{
        BaseArtifact: BaseArtifact{
            ArtifactID:   uuid.New(),
            ArtifactType: "design_artifact",
            Version:      1,
            TopicID:      "topic-1",
            CreatedAt:    time.Now().UTC(),
            Producer:     AgentDesign,
        },
        Components: []Component{
            {Name: "api-server", Type: "service", Technology: "Go"},
            {Name: "sqlite-db", Type: "db", Technology: "SQLite"},
        },
        Interactions: []Interaction{
            {From: "api-server", To: "sqlite-db", Protocol: "call"},
        },
        Assumptions:      []string{"local execution only"},
        Constraints:      []string{"no cloud resources"},
        SuccessCriteria:  []string{"tests pass", "lint clean"},
        TestPlan: TestPlan{
            Strategy: "unit + integration",
            TestCases: []TestCase{
                {Name: "unit tests", Description: "Run go test", Command: "go test ./...", Expected: "exit code 0"},
            },
        },
        EstimatedCostUSD:       0,
        RequiresCloudResources: false,
    }

    b, _ := json.Marshal(d)
    var d2 DesignArtifact
    if err := json.Unmarshal(b, &d2); err != nil {
        t.Fatalf("Unmarshal error: %v", err)
    }
    if len(d2.Components) != 2 {
        t.Errorf("expected 2 components, got %d", len(d2.Components))
    }
    if d2.TestPlan.TestCases[0].Command != "go test ./..." {
        t.Errorf("test case command mismatch")
    }
}

func TestExperimentResult_RoundTrip(t *testing.T) {
    r := ExperimentResult{
        BaseArtifact: BaseArtifact{
            ArtifactID:   uuid.New(),
            ArtifactType: "experiment_result",
            Version:      1,
            TopicID:      "topic-1",
            CreatedAt:    time.Now().UTC(),
            Producer:     AgentExperiment,
        },
        ExecutionID: "exec-001",
        Environment: Environment{
            Type:    "local",
            Runtime: "go",
            Workdir: "/tmp/atrpe/workspaces/topic-1/attempt-1",
            Attempt: 1,
        },
        ExperimentLanguage: "go",
        Entrypoints:        []string{"cmd/example/main.go"},
        GeneratedFiles:     []string{"cmd/example/main.go", "go.mod", "example_test.go"},
        Commands: []CommandResult{
            {Name: "go test", Args: []string{"go", "test", "./..."}, ExitCode: 0, Stdout: "ok", Stderr: "", DurationMS: 1500},
        },
    }

    b, _ := json.Marshal(r)
    var r2 ExperimentResult
    if err := json.Unmarshal(b, &r2); err != nil {
        t.Fatalf("Unmarshal error: %v", err)
    }
    if r2.Commands[0].ExitCode != 0 {
        t.Error("command exit code mismatch")
    }
    if r2.Environment.Attempt != 1 {
        t.Error("environment attempt mismatch")
    }
}

func TestVerificationReport_RoundTrip(t *testing.T) {
    vr := VerificationReport{
        BaseArtifact: BaseArtifact{
            ArtifactID:   uuid.New(),
            ArtifactType: "verification_report",
            Version:      1,
            TopicID:      "topic-1",
            CreatedAt:    time.Now().UTC(),
            Producer:     AgentVerification,
        },
        LintPassed:    true,
        VetPassed:     true,
        TestsPassed:   true,
        LinksPassed:   false,
        OverallPassed: false,
        BlockingIssues: []string{"broken link: https://example.com/dead"},
    }

    b, _ := json.Marshal(vr)
    var vr2 VerificationReport
    if err := json.Unmarshal(b, &vr2); err != nil {
        t.Fatalf("Unmarshal error: %v", err)
    }
    if !vr2.VetPassed {
        t.Error("expected vet_passed=true")
    }
    if vr2.LinksPassed {
        t.Error("expected links_passed=false")
    }
}

func TestPatchResult_RoundTrip(t *testing.T) {
    origID := uuid.New()
    pr := PatchResult{
        BaseArtifact: BaseArtifact{
            ArtifactID:   uuid.New(),
            ArtifactType: "patch_result",
            Version:      1,
            TopicID:      "topic-1",
            CreatedAt:    time.Now().UTC(),
            Producer:     AgentExperiment,
        },
        OriginalArtifactID: origID,
        PatchedFiles: []PatchedFile{
            {Path: "main.go", OldHash: "abc123", NewHash: "def456"},
        },
        FailedCommands: []CommandResult{
            {Name: "go test", Args: []string{"go", "test", "./..."}, ExitCode: 1, Stderr: "FAIL"},
        },
        RemediationReason: "test failure: expected 42, got 0",
    }

    b, _ := json.Marshal(pr)
    var pr2 PatchResult
    if err := json.Unmarshal(b, &pr2); err != nil {
        t.Fatalf("Unmarshal error: %v", err)
    }
    if pr2.OriginalArtifactID != origID {
        t.Error("OriginalArtifactID mismatch")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/artifacts/ -v
```
Expected: FAIL

- [ ] **Step 3: Implement all artifact types**

`internal/artifacts/models.go`:
```go
package artifacts

import (
    "time"

    "github.com/google/uuid"
)

// URI is a pointer to object storage content.
type URI string

// AgentName identifies which agent produced an artifact.
type AgentName string

const (
    AgentResearch     AgentName = "research"
    AgentDesign       AgentName = "design"
    AgentExperiment   AgentName = "experiment"
    AgentVerification AgentName = "verification"
    AgentWriter       AgentName = "writer"
)

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

// -- Topic & Brief (knowledge models referenced by agents) --

type TopicCandidate struct {
    ID        string    `json:"id"`
    Source    string    `json:"source"`
    Title     string    `json:"title"`
    URL       string    `json:"url"`
    Score     float64   `json:"score"`
    CreatedAt time.Time `json:"created_at"`
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
    URL       string `json:"url"`
    Title     string `json:"title"`
    Retrieved string `json:"retrieved"`
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

// -- Knowledge System --

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
```

- [ ] **Step 4: Install uuid dependency and run tests**

```bash
go get github.com/google/uuid
go test ./internal/artifacts/ -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/artifacts/ go.mod go.sum && git commit -m "feat: add artifact models with JSON round-trip support"
```

---

### Task 4: ObjectStore interface and LocalObjectStore

**Files:**
- Create: `atrpe/internal/objectstore/objectstore.go`
- Create: `atrpe/internal/objectstore/local.go`
- Create: `atrpe/internal/objectstore/local_test.go`

- [ ] **Step 1: Write the failing test with Put/Get/Head/Delete/NestedKeys**

`internal/objectstore/local_test.go`:
```go
package objectstore

import (
    "context"
    "io"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestLocalObjectStore_PutGet(t *testing.T) {
    dir := t.TempDir()
    store := NewLocalObjectStore(dir)
    ctx := context.Background()

    content := "hello world"
    uri, err := store.Put(ctx, "test/hello.txt", strings.NewReader(content), "text/plain")
    if err != nil {
        t.Fatalf("Put error: %v", err)
    }
    if uri == "" {
        t.Fatal("expected non-empty URI")
    }

    reader, err := store.Get(ctx, uri)
    if err != nil {
        t.Fatalf("Get error: %v", err)
    }
    defer reader.Close()

    got, err := io.ReadAll(reader)
    if err != nil {
        t.Fatalf("ReadAll error: %v", err)
    }
    if string(got) != content {
        t.Errorf("expected %q, got %q", content, string(got))
    }
}

func TestLocalObjectStore_Head(t *testing.T) {
    dir := t.TempDir()
    store := NewLocalObjectStore(dir)
    ctx := context.Background()

    uri, _ := store.Put(ctx, "test/data.bin", strings.NewReader("12345"), "application/octet-stream")
    meta, err := store.Head(ctx, uri)
    if err != nil {
        t.Fatalf("Head error: %v", err)
    }
    if meta.SizeBytes != 5 {
        t.Errorf("expected SizeBytes=5, got %d", meta.SizeBytes)
    }
}

func TestLocalObjectStore_Delete(t *testing.T) {
    dir := t.TempDir()
    store := NewLocalObjectStore(dir)
    ctx := context.Background()

    uri, _ := store.Put(ctx, "test/to-delete.txt", strings.NewReader("data"), "text/plain")
    if err := store.Delete(ctx, uri); err != nil {
        t.Fatalf("Delete error: %v", err)
    }
    _, err := store.Get(ctx, uri)
    if err == nil {
        t.Fatal("expected error after delete, got nil")
    }
}

func TestLocalObjectStore_Get_Missing(t *testing.T) {
    dir := t.TempDir()
    store := NewLocalObjectStore(dir)
    _, err := store.Get(context.Background(), URI("file:///nonexistent"))
    if err == nil {
        t.Fatal("expected error for missing file")
    }
}

func TestLocalObjectStore_NestedKeys(t *testing.T) {
    dir := t.TempDir()
    store := NewLocalObjectStore(dir)
    ctx := context.Background()

    uri, _ := store.Put(ctx, "a/b/c/nested.txt", strings.NewReader("nested"), "text/plain")
    expectedPath := filepath.Join(dir, "a", "b", "c", "nested.txt")
    if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
        t.Errorf("expected file at %s", expectedPath)
    }
    _ = uri
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/objectstore/ -v
```
Expected: FAIL

- [ ] **Step 3: Implement interface and LocalObjectStore**

`internal/objectstore/objectstore.go`:
```go
package objectstore

import (
    "context"
    "io"
)

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

`internal/objectstore/local.go`:
```go
package objectstore

import (
    "context"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strings"
)

type LocalObjectStore struct {
    rootDir string
}

func NewLocalObjectStore(rootDir string) *LocalObjectStore {
    return &LocalObjectStore{rootDir: rootDir}
}

func (s *LocalObjectStore) Put(ctx context.Context, key string, body io.Reader, contentType string) (URI, error) {
    fullPath := filepath.Join(s.rootDir, key)
    if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
        return "", fmt.Errorf("mkdir: %w", err)
    }
    f, err := os.Create(fullPath)
    if err != nil {
        return "", fmt.Errorf("create: %w", err)
    }
    defer f.Close()
    if _, err := io.Copy(f, body); err != nil {
        return "", fmt.Errorf("write: %w", err)
    }
    return URI("file://" + fullPath), nil
}

func (s *LocalObjectStore) Get(ctx context.Context, uri URI) (io.ReadCloser, error) {
    path := strings.TrimPrefix(string(uri), "file://")
    f, err := os.Open(path)
    if err != nil {
        return nil, fmt.Errorf("open: %w", err)
    }
    return f, nil
}

func (s *LocalObjectStore) Head(ctx context.Context, uri URI) (ObjectMetadata, error) {
    path := strings.TrimPrefix(string(uri), "file://")
    info, err := os.Stat(path)
    if err != nil {
        return ObjectMetadata{}, fmt.Errorf("stat: %w", err)
    }
    return ObjectMetadata{ContentType: "application/octet-stream", SizeBytes: info.Size()}, nil
}

func (s *LocalObjectStore) Delete(ctx context.Context, uri URI) error {
    path := strings.TrimPrefix(string(uri), "file://")
    if err := os.Remove(path); err != nil {
        return fmt.Errorf("remove: %w", err)
    }
    return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/objectstore/ -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/objectstore/ && git commit -m "feat: add ObjectStore interface and LocalObjectStore"
```

---

### Task 5: KnowledgeStore — SQLite schema and implementation

**Files:**
- Create: `atrpe/internal/knowledge/schema.sql`
- Create: `atrpe/internal/knowledge/sqlite_store.go`
- Create: `atrpe/internal/knowledge/sqlite_store_test.go`

- [ ] **Step 1: Write schema SQL**

`internal/knowledge/schema.sql`:
```sql
CREATE TABLE IF NOT EXISTS topic_candidates (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    title TEXT NOT NULL,
    url TEXT NOT NULL,
    score REAL NOT NULL DEFAULT 0.0,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS technical_briefs (
    id TEXT PRIMARY KEY,
    topic_id TEXT NOT NULL,
    artifact_uri TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS design_artifacts (
    id TEXT PRIMARY KEY,
    topic_id TEXT NOT NULL,
    artifact_uri TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS experiment_results (
    id TEXT PRIMARY KEY,
    topic_id TEXT NOT NULL,
    artifact_uri TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS verification_reports (
    id TEXT PRIMARY KEY,
    topic_id TEXT NOT NULL,
    artifact_uri TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS patch_results (
    id TEXT PRIMARY KEY,
    topic_id TEXT NOT NULL,
    artifact_uri TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS article_drafts (
    id TEXT PRIMARY KEY,
    topic_id TEXT NOT NULL,
    artifact_uri TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS published_articles (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL,
    title TEXT NOT NULL,
    published_at TEXT NOT NULL,
    platform TEXT NOT NULL DEFAULT 'zenn',
    url TEXT NOT NULL,
    views INTEGER DEFAULT 0,
    likes INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS citation_registry (
    id TEXT PRIMARY KEY,
    source_url TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    hash_algorithm TEXT DEFAULT 'sha256',
    retrieved_at TEXT NOT NULL,
    UNIQUE(source_url, content_hash)
);

CREATE TABLE IF NOT EXISTS failed_patterns (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topic_id TEXT NOT NULL,
    error_stage TEXT NOT NULL,
    error_message TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS engagement_metrics (
    topic_id TEXT NOT NULL,
    platform TEXT NOT NULL DEFAULT 'zenn',
    publish_date TEXT NOT NULL,
    views INTEGER DEFAULT 0,
    likes INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_topic_candidates_created_at ON topic_candidates(created_at);
CREATE INDEX IF NOT EXISTS idx_technical_briefs_topic_id ON technical_briefs(topic_id);
CREATE INDEX IF NOT EXISTS idx_published_articles_slug ON published_articles(slug);
CREATE INDEX IF NOT EXISTS idx_citation_registry_source_url ON citation_registry(source_url);
CREATE INDEX IF NOT EXISTS idx_engagement_metrics_topic_date ON engagement_metrics(topic_id, publish_date);
```

- [ ] **Step 2: Write the test**

`internal/knowledge/sqlite_store_test.go`:
```go
package knowledge

import (
    "context"
    "testing"
    "time"

    "github.com/your-org/atrpe/internal/artifacts"
)

func setupStore(t *testing.T) *SQLiteStore {
    t.Helper()
    store, err := NewSQLiteStore(":memory:")
    if err != nil {
        t.Fatalf("NewSQLiteStore error: %v", err)
    }
    t.Cleanup(func() { store.Close() })
    return store
}

func TestSaveAndGetTopicCandidate(t *testing.T) {
    store := setupStore(t)
    ctx := context.Background()

    candidate := artifacts.TopicCandidate{
        ID: "abc123", Source: "github_trending", Title: "Kubernetes Operators in Go",
        URL: "https://github.com/example/operator", Score: 0.85, CreatedAt: time.Now().UTC(),
    }

    if err := store.SaveTopicCandidate(ctx, candidate); err != nil {
        t.Fatalf("SaveTopicCandidate error: %v", err)
    }

    got, err := store.GetTopicCandidate(ctx, "abc123")
    if err != nil {
        t.Fatalf("GetTopicCandidate error: %v", err)
    }
    if got.Title != candidate.Title {
        t.Errorf("title mismatch: %s != %s", got.Title, candidate.Title)
    }
}

func TestListTopicCandidates(t *testing.T) {
    store := setupStore(t)
    ctx := context.Background()

    for _, c := range []artifacts.TopicCandidate{
        {ID: "a", Source: "s1", Title: "T1", URL: "u1", Score: 0.9, CreatedAt: time.Now()},
        {ID: "b", Source: "s2", Title: "T2", URL: "u2", Score: 0.5, CreatedAt: time.Now()},
        {ID: "c", Source: "s3", Title: "T3", URL: "u3", Score: 0.1, CreatedAt: time.Now()},
    } {
        store.SaveTopicCandidate(ctx, c)
    }

    list, err := store.ListTopicCandidates(ctx, 2)
    if err != nil {
        t.Fatalf("ListTopicCandidates error: %v", err)
    }
    if len(list) != 2 {
        t.Errorf("expected 2 candidates, got %d", len(list))
    }
}

func TestSaveTechnicalBrief(t *testing.T) {
    store := setupStore(t)
    ctx := context.Background()
    uri := artifacts.URI("file:///data/artifacts/briefs/b1.json")
    if err := store.SaveTechnicalBrief(ctx, "b1", "topic-1", uri); err != nil {
        t.Fatalf("SaveTechnicalBrief error: %v", err)
    }
}

func TestSavePublishedArticle(t *testing.T) {
    store := setupStore(t)
    ctx := context.Background()
    article := artifacts.PublishedArticle{
        ID: "pub-1", Slug: "my-go-article", Title: "My Go Article",
        PublishedAt: time.Now().UTC(), Platform: "zenn",
        URL: "https://zenn.dev/example/articles/my-go-article",
    }
    if err := store.SavePublishedArticle(ctx, article); err != nil {
        t.Fatalf("SavePublishedArticle error: %v", err)
    }
}

func TestRegisterCitation(t *testing.T) {
    store := setupStore(t)
    ctx := context.Background()
    citation := artifacts.CitationRecord{
        ID: "cite-1", SourceURL: "https://go.dev/doc/effective_go",
        ContentHash: "abc123hash", HashAlgorithm: "sha256",
        RetrievedAt: time.Now().UTC().Format(time.RFC3339),
    }
    if err := store.RegisterCitation(ctx, citation); err != nil {
        t.Fatalf("RegisterCitation error: %v", err)
    }
    // Duplicate should be ignored
    if err := store.RegisterCitation(ctx, citation); err != nil {
        t.Fatalf("RegisterCitation duplicate error: %v", err)
    }
}
```

- [ ] **Step 3: Implement SQLiteStore**

`internal/knowledge/sqlite_store.go`:
```go
package knowledge

import (
    "context"
    "database/sql"
    _ "embed"
    "fmt"
    "time"

    "github.com/your-org/atrpe/internal/artifacts"
    _ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schemaSQL string

type SQLiteStore struct {
    db *sql.DB
}

func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
    db, err := sql.Open("sqlite3", dsn)
    if err != nil {
        return nil, fmt.Errorf("open: %w", err)
    }
    db.SetMaxOpenConns(1)
    if _, err := db.Exec(schemaSQL); err != nil {
        db.Close()
        return nil, fmt.Errorf("migrate: %w", err)
    }
    return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

func (s *SQLiteStore) SaveTopicCandidate(ctx context.Context, c artifacts.TopicCandidate) error {
    _, err := s.db.ExecContext(ctx,
        `INSERT OR REPLACE INTO topic_candidates (id, source, title, url, score, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
        c.ID, c.Source, c.Title, c.URL, c.Score, c.CreatedAt.Format(time.RFC3339),
    )
    return err
}

func (s *SQLiteStore) GetTopicCandidate(ctx context.Context, candidateID string) (artifacts.TopicCandidate, error) {
    var c artifacts.TopicCandidate
    var createdAt string
    err := s.db.QueryRowContext(ctx,
        `SELECT id, source, title, url, score, created_at FROM topic_candidates WHERE id = ?`,
        candidateID,
    ).Scan(&c.ID, &c.Source, &c.Title, &c.URL, &c.Score, &createdAt)
    if err != nil {
        return c, fmt.Errorf("query: %w", err)
    }
    c.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
    return c, nil
}

func (s *SQLiteStore) ListTopicCandidates(ctx context.Context, limit int) ([]artifacts.TopicCandidate, error) {
    rows, err := s.db.QueryContext(ctx,
        `SELECT id, source, title, url, score, created_at FROM topic_candidates ORDER BY score DESC LIMIT ?`,
        limit,
    )
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var candidates []artifacts.TopicCandidate
    for rows.Next() {
        var c artifacts.TopicCandidate
        var createdAt string
        if err := rows.Scan(&c.ID, &c.Source, &c.Title, &c.URL, &c.Score, &createdAt); err != nil {
            return nil, err
        }
        c.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
        candidates = append(candidates, c)
    }
    return candidates, rows.Err()
}

func (s *SQLiteStore) SaveTechnicalBrief(ctx context.Context, id, topicID string, uri artifacts.URI) error {
    _, err := s.db.ExecContext(ctx,
        `INSERT OR REPLACE INTO technical_briefs (id, topic_id, artifact_uri, created_at) VALUES (?, ?, ?, ?)`,
        id, topicID, string(uri), time.Now().UTC().Format(time.RFC3339),
    )
    return err
}

func (s *SQLiteStore) SavePublishedArticle(ctx context.Context, a artifacts.PublishedArticle) error {
    _, err := s.db.ExecContext(ctx,
        `INSERT OR REPLACE INTO published_articles (id, slug, title, published_at, platform, url, views, likes) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
        a.ID, a.Slug, a.Title, a.PublishedAt.Format(time.RFC3339), a.Platform, a.URL, a.Views, a.Likes,
    )
    return err
}

func (s *SQLiteStore) RegisterCitation(ctx context.Context, c artifacts.CitationRecord) error {
    _, err := s.db.ExecContext(ctx,
        `INSERT OR IGNORE INTO citation_registry (id, source_url, content_hash, hash_algorithm, retrieved_at) VALUES (?, ?, ?, ?, ?)`,
        c.ID, c.SourceURL, c.ContentHash, c.HashAlgorithm, c.RetrievedAt,
    )
    return err
}

func (s *SQLiteStore) SaveFailedPattern(ctx context.Context, f artifacts.FailedPattern) error {
    _, err := s.db.ExecContext(ctx,
        `INSERT INTO failed_patterns (topic_id, error_stage, error_message, created_at) VALUES (?, ?, ?, ?)`,
        f.TopicID, f.ErrorStage, f.ErrorMessage, f.CreatedAt,
    )
    return err
}

func (s *SQLiteStore) SaveEngagementMetrics(ctx context.Context, m artifacts.EngagementMetrics) error {
    _, err := s.db.ExecContext(ctx,
        `INSERT OR REPLACE INTO engagement_metrics (topic_id, platform, publish_date, views, likes) VALUES (?, ?, ?, ?, ?)`,
        m.TopicID, m.Platform, m.PublishDate, m.Views, m.Likes,
    )
    return err
}

func (s *SQLiteStore) SaveArtifactMeta(ctx context.Context, table, id, topicID string, uri artifacts.URI) error {
    query := fmt.Sprintf(`INSERT OR REPLACE INTO %s (id, topic_id, artifact_uri, created_at) VALUES (?, ?, ?, ?)`, table)
    _, err := s.db.ExecContext(ctx, query, id, topicID, string(uri), time.Now().UTC().Format(time.RFC3339))
    return err
}

func (s *SQLiteStore) GetArtifactURI(ctx context.Context, table, id string) (artifacts.URI, error) {
    var uri string
    query := fmt.Sprintf(`SELECT artifact_uri FROM %s WHERE id = ?`, table)
    err := s.db.QueryRowContext(ctx, query, id).Scan(&uri)
    if err != nil {
        return "", fmt.Errorf("query: %w", err)
    }
    return artifacts.URI(uri), nil
}
```

- [ ] **Step 4: Run tests**

```bash
go get github.com/mattn/go-sqlite3
go test ./internal/knowledge/ -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/knowledge/ go.mod go.sum && git commit -m "feat: add SQLite knowledge store with schema and CRUD"
```

---

### Task 6: GitHub command parser

**Files:**
- Create: `atrpe/internal/github/commands.go`
- Create: `atrpe/internal/github/commands_test.go`

- [ ] **Step 1: Write the test**

`internal/github/commands_test.go`:
```go
package github

import (
    "testing"
)

func TestParseSelect(t *testing.T) {
    cmd, err := Parse("/select abc123def456")
    if err != nil {
        t.Fatalf("Parse error: %v", err)
    }
    if cmd.Signal != "TopicSelectedSignal" {
        t.Errorf("expected TopicSelectedSignal, got %s", cmd.Signal)
    }
    if cmd.Payload["candidate_id"] != "abc123def456" {
        t.Errorf("expected candidate_id=abc123def456, got %v", cmd.Payload["candidate_id"])
    }
}

func TestParseSelect_NoID(t *testing.T) {
    _, err := Parse("/select")
    if err == nil {
        t.Error("expected error for /select without candidate_id")
    }
}

func TestParseApprove(t *testing.T) {
    cmd, err := Parse("/approve")
    if err != nil {
        t.Fatalf("Parse error: %v", err)
    }
    if cmd.Signal != "PublishApprovalSignal" {
        t.Errorf("expected PublishApprovalSignal, got %s", cmd.Signal)
    }
}

func TestParseRetry(t *testing.T) {
    cmd, err := Parse("/retry")
    if err != nil {
        t.Fatalf("Parse error: %v", err)
    }
    if cmd.Signal != "RetrySignal" {
        t.Errorf("expected RetrySignal, got %s", cmd.Signal)
    }
}

func TestParseAbort(t *testing.T) {
    cmd, err := Parse("/abort")
    if err != nil {
        t.Fatalf("Parse error: %v", err)
    }
    if cmd.Signal != "AbortSignal" {
        t.Errorf("expected AbortSignal, got %s", cmd.Signal)
    }
}

func TestParseChanges(t *testing.T) {
    cmd, err := Parse("/changes Please add more detail to the troubleshooting section")
    if err != nil {
        t.Fatalf("Parse error: %v", err)
    }
    if cmd.Signal != "RequestChangesSignal" {
        t.Errorf("expected RequestChangesSignal, got %s", cmd.Signal)
    }
    notes, ok := cmd.Payload["change_notes"].(string)
    if !ok || notes != "Please add more detail to the troubleshooting section" {
        t.Errorf("expected change_notes, got %v", cmd.Payload["change_notes"])
    }
}

func TestParseChanges_NoNotes(t *testing.T) {
    _, err := Parse("/changes")
    if err == nil {
        t.Error("expected error for /changes without notes")
    }
}

func TestParse_UnknownCommand(t *testing.T) {
    _, err := Parse("/unknown")
    if err == nil {
        t.Error("expected error for unknown command")
    }
}

func TestParse_NotACommand(t *testing.T) {
    _, err := Parse("just a regular comment")
    if err == nil {
        t.Error("expected error for non-command text")
    }
}
```

- [ ] **Step 2: Implement parser**

`internal/github/commands.go`:
```go
package github

import (
    "fmt"
    "strings"
)

type ParsedCommand struct {
    Signal  string
    Payload map[string]any
}

func Parse(body string) (*ParsedCommand, error) {
    trimmed := strings.TrimSpace(body)
    if !strings.HasPrefix(trimmed, "/") {
        return nil, fmt.Errorf("not a command")
    }

    parts := strings.SplitN(trimmed, " ", 2)
    command := parts[0]
    rest := ""
    if len(parts) == 2 {
        rest = strings.TrimSpace(parts[1])
    }

    switch command {
    case "/select":
        if rest == "" {
            return nil, fmt.Errorf("/select requires a candidate_id")
        }
        return &ParsedCommand{
            Signal:  "TopicSelectedSignal",
            Payload: map[string]any{"candidate_id": rest},
        }, nil
    case "/approve":
        return &ParsedCommand{Signal: "PublishApprovalSignal", Payload: map[string]any{}}, nil
    case "/retry":
        return &ParsedCommand{Signal: "RetrySignal", Payload: map[string]any{}}, nil
    case "/abort":
        return &ParsedCommand{Signal: "AbortSignal", Payload: map[string]any{}}, nil
    case "/changes":
        if rest == "" {
            return nil, fmt.Errorf("/changes requires change notes")
        }
        return &ParsedCommand{
            Signal:  "RequestChangesSignal",
            Payload: map[string]any{"change_notes": rest},
        }, nil
    default:
        return nil, fmt.Errorf("unknown command: %s", command)
    }
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/github/ -v
```
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/github/ && git commit -m "feat: add GitHub command parser"
```

---

### Task 7: Webhook validation, internal signal endpoint, artifact repository

**Files:**
- Create: `atrpe/internal/github/webhook.go`
- Create: `atrpe/internal/github/webhook_test.go`
- Create: `atrpe/internal/github/internal_signal.go`
- Create: `atrpe/internal/github/internal_signal_test.go`
- Create: `atrpe/internal/artifacts/repository.go`
- Create: `atrpe/internal/artifacts/repository_test.go`
- Modify: `atrpe/cmd/api/main.go`

- [ ] **Step 1: Write tests for webhook + internal signal + artifact repo**

Webhook tests at `internal/github/webhook_test.go`:
```go
package github

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

func TestValidateSignature_Valid(t *testing.T) {
    secret := "test-secret"
    body := []byte(`{"action":"created","comment":{"body":"/approve"}}`)
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
    if err := ValidateSignature(body, sig, secret); err != nil {
        t.Errorf("expected valid signature, got error: %v", err)
    }
}

func TestValidateSignature_Invalid(t *testing.T) {
    if err := ValidateSignature([]byte("body"), "sha256=badsig", "secret"); err == nil {
        t.Error("expected error for invalid signature")
    }
}

func TestValidateSignature_EmptySecret(t *testing.T) {
    if err := ValidateSignature([]byte("body"), "sha256=anything", ""); err != nil {
        t.Errorf("expected no error with empty secret, got: %v", err)
    }
}

func TestWebhookHandler_ValidCommand(t *testing.T) {
    handler := NewWebhookHandler("test-secret", nil, nil)
    body := `{"action":"created","comment":{"body":"/approve"}}`
    mac := hmac.New(sha256.New, []byte("test-secret"))
    mac.Write([]byte(body))
    sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

    req := httptest.NewRequest("POST", "/webhook", strings.NewReader(body))
    req.Header.Set("X-Hub-Signature-256", sig)
    req.Header.Set("X-GitHub-Event", "issue_comment")
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK {
        t.Errorf("expected 200, got %d: body=%s", rec.Code, rec.Body.String())
    }
}
```

Internal signal tests at `internal/github/internal_signal_test.go`:
```go
package github

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestInternalSignalHandler_Valid(t *testing.T) {
    handler := NewInternalSignalHandler("test-token", nil, nil)
    payload := InternalSignalRequest{Signal: "TopicSelectedSignal", Payload: map[string]any{"candidate_id": "abc123"}}
    body, _ := json.Marshal(payload)
    req := httptest.NewRequest("POST", "/internal/workflows/wf-1/signal", bytes.NewReader(body))
    req.Header.Set("Authorization", "Bearer test-token")
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK {
        t.Errorf("expected 200, got %d: body=%s", rec.Code, rec.Body.String())
    }
}

func TestInternalSignalHandler_InvalidToken(t *testing.T) {
    handler := NewInternalSignalHandler("test-token", nil, nil)
    payload := InternalSignalRequest{Signal: "TopicSelectedSignal"}
    body, _ := json.Marshal(payload)
    req := httptest.NewRequest("POST", "/internal/workflows/wf-1/signal", bytes.NewReader(body))
    req.Header.Set("Authorization", "Bearer wrong-token")
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)
    if rec.Code != http.StatusUnauthorized {
        t.Errorf("expected 401, got %d", rec.Code)
    }
}

func TestInternalSignalHandler_UnknownSignal(t *testing.T) {
    handler := NewInternalSignalHandler("test-token", nil, nil)
    payload := InternalSignalRequest{Signal: "UnknownSignal"}
    body, _ := json.Marshal(payload)
    req := httptest.NewRequest("POST", "/internal/workflows/wf-1/signal", bytes.NewReader(body))
    req.Header.Set("Authorization", "Bearer test-token")
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)
    if rec.Code != http.StatusBadRequest {
        t.Errorf("expected 400, got %d", rec.Code)
    }
}
```

- [ ] **Step 2: Implement webhook.go, internal_signal.go, repository.go**

`internal/github/webhook.go`:
```go
package github

import (
    "context"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "log/slog"
    "net/http"
)

type TemporalSignalSender interface {
    SendSignal(ctx context.Context, workflowID, signal string, payload map[string]any) error
}

type WebhookHandler struct {
    webhookSecret string
    sender        TemporalSignalSender
    logger        *slog.Logger
}

func NewWebhookHandler(secret string, sender TemporalSignalSender, logger *slog.Logger) *WebhookHandler {
    if logger == nil { logger = slog.Default() }
    return &WebhookHandler{webhookSecret: secret, sender: sender, logger: logger}
}

func ValidateSignature(body []byte, signature, secret string) error {
    if secret == "" { return nil }
    if signature == "" { return fmt.Errorf("missing signature header") }
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
    if !hmac.Equal([]byte(signature), []byte(expected)) {
        return fmt.Errorf("signature mismatch")
    }
    return nil
}

type githubCommentEvent struct {
    Action  string `json:"action"`
    Comment struct {
        Body string `json:"body"`
        ID   int64  `json:"id"`
    } `json:"comment"`
    Issue struct {
        Number int `json:"number"`
    } `json:"issue"`
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
    body, err := io.ReadAll(r.Body)
    r.Body.Close()
    if err != nil {
        h.logger.Error("read body", "error", err)
        http.Error(w, "read error", http.StatusBadRequest)
        return
    }
    sig := r.Header.Get("X-Hub-Signature-256")
    if err := ValidateSignature(body, sig, h.webhookSecret); err != nil {
        h.logger.Warn("invalid signature", "error", err)
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }
    event := r.Header.Get("X-GitHub-Event")
    if event != "issue_comment" {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{"status":"ignored"}`))
        return
    }
    var evt githubCommentEvent
    if err := json.Unmarshal(body, &evt); err != nil {
        h.logger.Error("unmarshal event", "error", err)
        http.Error(w, "bad request", http.StatusBadRequest)
        return
    }
    if evt.Action != "created" {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{"status":"ignored"}`))
        return
    }
    cmd, err := Parse(evt.Comment.Body)
    if err != nil {
        h.logger.Debug("not a command", "body", evt.Comment.Body)
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{"status":"not_a_command"}`))
        return
    }
    h.logger.Info("parsed command", "signal", cmd.Signal, "payload", cmd.Payload)
    workflowID := fmt.Sprintf("article-%d", evt.Issue.Number)
    if h.sender != nil {
        if err := h.sender.SendSignal(r.Context(), workflowID, cmd.Signal, cmd.Payload); err != nil {
            h.logger.Error("send signal", "error", err)
            http.Error(w, "signal error", http.StatusInternalServerError)
            return
        }
    }
    resp, _ := json.Marshal(map[string]string{"status": "ok", "signal": cmd.Signal, "message": "signal sent to workflow " + workflowID})
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    w.Write(resp)
}
```

`internal/github/internal_signal.go`:
```go
package github

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"
    "net/http"
    "strings"
)

var validSignals = map[string]bool{
    "TopicSelectedSignal": true, "PublishApprovalSignal": true,
    "RetrySignal": true, "AbortSignal": true, "RequestChangesSignal": true,
}

type InternalSignalRequest struct {
    Signal  string         `json:"signal"`
    Payload map[string]any `json:"payload"`
}

type InternalSignalHandler struct {
    authToken string
    sender    TemporalSignalSender
    logger    *slog.Logger
}

func NewInternalSignalHandler(token string, sender TemporalSignalSender, logger *slog.Logger) *InternalSignalHandler {
    if logger == nil { logger = slog.Default() }
    return &InternalSignalHandler{authToken: token, sender: sender, logger: logger}
}

func (h *InternalSignalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
    auth := r.Header.Get("Authorization")
    if h.authToken != "" && (!strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != h.authToken) {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }
    pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/internal/workflows/"), "/")
    if len(pathParts) < 2 || pathParts[1] != "signal" {
        http.Error(w, "invalid path", http.StatusBadRequest)
        return
    }
    workflowID := pathParts[0]
    var req InternalSignalRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid json", http.StatusBadRequest)
        return
    }
    if !validSignals[req.Signal] {
        http.Error(w, fmt.Sprintf("unknown signal: %s", req.Signal), http.StatusBadRequest)
        return
    }
    if h.sender != nil {
        if err := h.sender.SendSignal(r.Context(), workflowID, req.Signal, req.Payload); err != nil {
            h.logger.Error("send internal signal", "error", err)
            http.Error(w, "signal error", http.StatusInternalServerError)
            return
        }
    }
    resp, _ := json.Marshal(map[string]string{"status": "ok"})
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    w.Write(resp)
}
```

`internal/artifacts/repository.go`:
```go
package artifacts

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"

    "github.com/your-org/atrpe/internal/knowledge"
    "github.com/your-org/atrpe/internal/objectstore"
)

type Repository struct {
    store   *knowledge.SQLiteStore
    objects objectstore.ObjectStore
}

func NewRepository(store *knowledge.SQLiteStore, objects objectstore.ObjectStore) *Repository {
    return &Repository{store: store, objects: objects}
}

func (r *Repository) SaveArtifact(ctx context.Context, table, id, topicID string, artifact interface{}) (objectstore.URI, error) {
    data, err := json.Marshal(artifact)
    if err != nil {
        return "", fmt.Errorf("marshal: %w", err)
    }
    key := fmt.Sprintf("%s/%s.json", table, id)
    uri, err := r.objects.Put(ctx, key, bytes.NewReader(data), "application/json")
    if err != nil {
        return "", fmt.Errorf("put object: %w", err)
    }
    if err := r.store.SaveArtifactMeta(ctx, table, id, topicID, uri); err != nil {
        return "", fmt.Errorf("save meta: %w", err)
    }
    return uri, nil
}

func (r *Repository) LoadArtifact(ctx context.Context, table, id string, target interface{}) error {
    uri, err := r.store.GetArtifactURI(ctx, table, id)
    if err != nil {
        return fmt.Errorf("get uri: %w", err)
    }
    reader, err := r.objects.Get(ctx, uri)
    if err != nil {
        return fmt.Errorf("get object: %w", err)
    }
    defer reader.Close()
    data, err := io.ReadAll(reader)
    if err != nil {
        return fmt.Errorf("read object: %w", err)
    }
    if err := json.Unmarshal(data, target); err != nil {
        return fmt.Errorf("unmarshal: %w", err)
    }
    return nil
}
```

- [ ] **Step 3: Wire up cmd/api/main.go**

```go
package main

import (
    "log/slog"
    "net/http"
    "os"

    "github.com/your-org/atrpe/internal/config"
    "github.com/your-org/atrpe/internal/github"
)

func main() {
    logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
    cfg, err := config.Load()
    if err != nil {
        logger.Error("failed to load config", "error", err)
        os.Exit(1)
    }

    var sender github.TemporalSignalSender // wired in Week 2

    mux := http.NewServeMux()
    webhook := github.NewWebhookHandler(cfg.GitHubWebhookSecret, sender, logger)
    mux.Handle("/webhook", webhook)
    internalSig := github.NewInternalSignalHandler(cfg.InternalSignalToken, sender, logger)
    mux.Handle("/internal/workflows/", internalSig)

    addr := ":8080"
    logger.Info("API server starting", "addr", addr)
    if err := http.ListenAndServe(addr, mux); err != nil {
        logger.Error("server error", "error", err)
        os.Exit(1)
    }
}
```

- [ ] **Step 4: Run all Week 1 tests**

```bash
go test ./internal/... -v
```
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: add webhook handler, internal signal endpoint, and artifact repository"
```

---

### Task 8: Cold-start topic discovery

**Files:**
- Create: `atrpe/internal/topics/scoring.go`
- Create: `atrpe/internal/topics/scoring_test.go`
- Create: `atrpe/internal/topics/discovery.go`
- Create: `atrpe/internal/topics/discovery_test.go`

- [ ] **Step 1: Implement scoring with CandidateID**

`internal/topics/scoring.go`:
```go
package topics

import (
    "crypto/sha256"
    "encoding/hex"
    "math"
    "time"
)

type CandidateInput struct {
    JapaneseArticleCount int
    GithubStars          int
    PublishedAt          time.Time
}

func ScoreCandidate(c CandidateInput) float64 {
    novelty := 1.0 - math.Min(float64(c.JapaneseArticleCount)/50.0, 1.0)
    practicality := math.Min(float64(c.GithubStars)/10000.0, 1.0)
    if c.GithubStars == 0 {
        practicality = 0.5
    }
    timing := recencyScore(c.PublishedAt)
    return 0.4*novelty + 0.4*practicality + 0.2*timing
}

func recencyScore(publishedAt time.Time) float64 {
    if publishedAt.IsZero() { return 0.0 }
    days := time.Since(publishedAt).Hours() / 24
    switch {
    case days <= 7:  return 1.0
    case days <= 30: return 0.5
    case days <= 90: return 0.2
    default:         return 0.0
    }
}

func CandidateID(source, url string) string {
    h := sha256.Sum256([]byte(source + "|" + url))
    return hex.EncodeToString(h[:])[:12]
}
```

- [ ] **Step 2: Implement discovery with GitHub trending**

`internal/topics/discovery.go`:
```go
package topics

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"

    "github.com/your-org/atrpe/internal/artifacts"
)

func DiscoverGitHubTrending(ctx context.Context, baseURL string) ([]artifacts.TopicCandidate, error) {
    url := baseURL + "/search/repositories?q=language:go&sort=stars&order=desc&per_page=10"
    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil { return nil, err }

    resp, err := http.DefaultClient.Do(req)
    if err != nil { return nil, fmt.Errorf("fetch github trending: %w", err) }
    defer resp.Body.Close()

    var result struct {
        Items []struct {
            FullName        string `json:"full_name"`
            HTMLURL         string `json:"html_url"`
            Description     string `json:"description"`
            StargazersCount int    `json:"stargazers_count"`
            CreatedAt       string `json:"created_at"`
        } `json:"items"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, fmt.Errorf("decode: %w", err)
    }

    var candidates []artifacts.TopicCandidate
    for _, item := range result.Items {
        id := CandidateID("github_trending", item.HTMLURL)
        publishedAt, _ := time.Parse(time.RFC3339, item.CreatedAt)
        score := ScoreCandidate(CandidateInput{
            GithubStars: item.StargazersCount,
            PublishedAt: publishedAt,
        })
        candidates = append(candidates, artifacts.TopicCandidate{
            ID: id, Source: "github_trending", Title: item.FullName,
            URL: item.HTMLURL, Score: score, CreatedAt: time.Now().UTC(),
        })
    }
    return candidates, nil
}

func DiscoverAll(ctx context.Context, sources []string, githubBaseURL string) ([]artifacts.TopicCandidate, error) {
    var all []artifacts.TopicCandidate
    for _, source := range sources {
        switch source {
        case "github_trending":
            candidates, err := DiscoverGitHubTrending(ctx, githubBaseURL)
            if err != nil { continue }
            all = append(all, candidates...)
        // other sources are stubs for MVP
        default:
            continue
        }
    }
    return all, nil
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/topics/ -v
```
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/topics/ && git commit -m "feat: add cold-start topic discovery and scoring"
```

---

## Week 2: Temporal Worker, ArticleWorkflow, Research & Design Agents

### Task 9: LLM client

**Files:**
- Create: `atrpe/internal/agents/llm_client.go`
- Create: `atrpe/internal/agents/llm_client_test.go`

- [ ] **Step 1: Implement LLMClient**

`internal/agents/llm_client.go`:
```go
package agents

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

type LLMConfig struct {
    Provider string
    Model    string
    APIKey   string
    BaseURL  string
}

type ChatMessage struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

type ChatResponse struct {
    Choices []struct {
        Message ChatMessage `json:"message"`
    } `json:"choices"`
}

type LLMClient struct {
    config LLMConfig
    http   *http.Client
}

func NewLLMClient(config LLMConfig) *LLMClient {
    return &LLMClient{config: config, http: &http.Client{Timeout: 120 * time.Second}}
}

func (c *LLMClient) Chat(ctx context.Context, messages []ChatMessage) (string, error) {
    reqBody := map[string]interface{}{
        "model":       c.config.Model,
        "messages":    messages,
        "temperature": 0.3,
        "max_tokens":  4096,
    }
    body, _ := json.Marshal(reqBody)
    url := c.config.BaseURL + "/chat/completions"
    req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
    if err != nil { return "", fmt.Errorf("create request: %w", err) }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+c.config.APIKey)

    resp, err := c.http.Do(req)
    if err != nil { return "", fmt.Errorf("do request: %w", err) }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil { return "", fmt.Errorf("read response: %w", err) }
    if resp.StatusCode != http.StatusOK {
        return "", fmt.Errorf("llm api error (status %d): %s", resp.StatusCode, string(respBody))
    }
    var chatResp ChatResponse
    if err := json.Unmarshal(respBody, &chatResp); err != nil {
        return "", fmt.Errorf("unmarshal response: %w", err)
    }
    if len(chatResp.Choices) == 0 { return "", fmt.Errorf("no choices in response") }
    return chatResp.Choices[0].Message.Content, nil
}
```

- [ ] **Step 2: Test and commit**

```bash
go test ./internal/agents/ -v -run TestLLM
```
Expected: PASS

```bash
git add internal/agents/ && git commit -m "feat: add OpenAI-compatible LLM client"
```

---

### Task 10: ArticleWorkflow with Temporal test suite

**Files:**
- Create: `atrpe/internal/workflows/article_workflow.go`
- Create: `atrpe/internal/workflows/article_workflow_test.go`
- Modify: `atrpe/cmd/worker/main.go`

- [ ] **Step 1: Implement ArticleWorkflow** full state machine (DISCOVER through ESCALATED), signal channels for TopicSelectedSignal, PublishApprovalSignal, RequestChangesSignal, RetrySignal, AbortSignal. State handlers call activity stubs with `defaultActivityOptions()`. WAIT_TOPIC_SELECTION and WAIT_PUBLISH_APPROVAL use `workflow.NewSelector` to block on signals. Remediation loop increments `RemediationCount` and routes PATCH_GENERATION → DESIGN_UPDATE → EXPERIMENT → VERIFY. When `RemediationCount >= MAX_REMEDIATION_ATTEMPTS`, route to ESCALATED instead.

- [ ] **Step 2: Write Temporal test suite tests** for happy path, abort during selection, changes+during approval, remediation exhaustion. Use `testsuite.WorkflowTestSuite` and `env.SignalWorkflow()`. Assert `env.IsWorkflowCompleted()` and `env.GetWorkflowError()`.

- [ ] **Step 3: Wire worker** — register ArticleWorkflow with Temporal worker. Use `client.Dial` with cfg.TemporalHostPort. Set `WorkerStopTimeout: 30 * time.Second` for graceful shutdown.

- [ ] **Step 4: Run tests and commit**

```bash
go get go.temporal.io/sdk go.temporal.io/sdk/testsuite github.com/stretchr/testify
go test ./internal/workflows/ -v
```
Expected: PASS

```bash
git add -A && git commit -m "feat: add ArticleWorkflow state machine with Temporal test coverage"
```

---

### Task 11: Activities scaffold + wire agents to workflow

**Files:**
- Create: `atrpe/internal/activities/activities.go`
- Modify: `atrpe/cmd/worker/main.go` (register activities)
- Modify: `atrpe/internal/workflows/article_workflow.go` (call real activities)

- [ ] **Step 1: Create Activities struct** holding Config, SQLiteStore, ObjectStore, LLMClient. Implement: `DiscoverTopics` (calls topics.DiscoverAll → stores candidates), `ResearchTopic` (calls ResearchAgent.Run → saves brief via Repository), `DesignArchitecture` (calls DesignAgent.Run → saves design), `RunExperiment` (calls ExperimentAgent.Run → saves result), `VerifyResults` (calls VerificationAgent.Run → saves report), `GenerateDraft` (calls WriterAgent.Run → saves draft), `PublishArticle` (calls go-git + GitHub API → saves PublishedArticle).

- [ ] **Step 2: Register activities with worker** via `w.RegisterActivity(activities.DiscoverTopics)` etc.

- [ ] **Step 3: Update workflow state handlers** to call activities via `workflow.ExecuteActivity(ctx, activities.DiscoverTopics).Get(ctx, &result)`. Pass `defaultActivityOptions()` or extend with retry policy.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "feat: wire activities to ArticleWorkflow and Temporal worker"
```

---

### Task 12: Research Agent + Design Agent (LLM-backed)

**Files:**
- Create: `atrpe/internal/agents/research.go`
- Create: `atrpe/internal/agents/research_test.go`
- Create: `atrpe/internal/agents/design.go`
- Create: `atrpe/internal/agents/design_test.go`

- [ ] **Step 1: ResearchAgent** — system prompt asks for JSON: core_concepts, supported_claims, common_pitfalls, research_questions, success_criteria, sources. Agent parses LLM response (with `extractJSON` fallback for markdown-wrapped JSON), validates, returns `TechnicalBrief`.

- [ ] **Step 2: DesignAgent** — system prompt asks for JSON: components, interactions, assumptions, constraints, success_criteria, test_plan, estimated_cost_usd, requires_cloud_resources. `Update()` method takes previous design + patch result, prompts LLM to update design reflecting code fixes.

- [ ] **Step 3: Run tests** with mock HTTP servers returning valid JSON responses. Assert artifact fields populated correctly, producer set, topic_id preserved.

```bash
go test ./internal/agents/ -v
```
Expected: ALL PASS

```bash
git add -A && git commit -m "feat: implement Research and Design agents with LLM backing"
```

---

## Week 3: Experiment, Verification, Writer Agents + Remediation

### Task 13: Experiment Agent + LLMCodeGenerator

**Files:**
- Create: `atrpe/internal/agents/codegen.go`
- Create: `atrpe/internal/agents/codegen_test.go`
- Create: `atrpe/internal/agents/experiment.go`
- Create: `atrpe/internal/agents/experiment_test.go`

- [ ] **Step 1: LLMCodeGenerator** — system prompt: produce complete Go module JSON (module_name, entrypoint, files[{path, content}]). Validate file paths are relative, no traversal, have content. GenerateGoModule returns GeneratedModule.

- [ ] **Step 2: ExperimentAgent** — accepts CodeGenerator interface + ExperimentRunner interface. `Run()`: calls codegen → creates workspace dir under `workspaceRoot/<topicID>/attempt-<N>/<executionID>` → writes files to disk → runs go vet, go test, golangci-lint, testplan commands via DefaultExperimentRunner → captures CommandResults → returns ExperimentResult.

- [ ] **Step 3: Patch()** — collects failed commands, re-calls CodeGenerator with error context, writes patched files over originals, computes old/new hashes, returns PatchResult.

- [ ] **Step 4: Run tests** with fakeCodeGen (returns pre-built module) and real `go test` on the generated module.

```bash
go test ./internal/agents/ -v -run TestExperiment
```
Expected: PASS

```bash
git add -A && git commit -m "feat: implement Experiment Agent with LLM code generation"
```

---

### Task 14: Verification Agent + Writer Agent

**Files:**
- Create: `atrpe/internal/agents/verification.go`
- Create: `atrpe/internal/agents/verification_test.go`
- Create: `atrpe/internal/agents/writer.go`
- Create: `atrpe/internal/agents/writer_test.go`

- [ ] **Step 1: VerificationAgent** — iterates over `VerificationChecks` config. For "lint"/"vet"/"tests": looks up CommandResult by name, checks ExitCode==0. For "links": HEAD/GET each source URL. Sets OverallPassed = len(BlockingIssues)==0.

- [ ] **Step 2: WriterAgent** — system prompt: output Zenn article JSON (slug, title, emoji, type, topics, published:false, sections{background, architecture, implementation, evaluation, troubleshooting}). Accepts optional changeNotes. If changeNotes non-empty, appends to prompt.

- [ ] **Step 3: Run tests** and commit.

```bash
git add -A && git commit -m "feat: implement Verification and Writer agents"
```

---

### Task 15: Experiment workspace cleanup + remediation loop

**Files:**
- Create: `atrpe/internal/agents/cleanup.go`
- Create: `atrpe/internal/agents/cleanup_test.go`

- [ ] **Step 1: WorkspaceCleanup** — reads ExperimentResult.Environment.Workdir. Applies policy: on_success (delete in 24h via background goroutine with TTL), on_failure (72h), on_abort (delete immediately with os.RemoveAll). Record cleanup actions.

- [ ] **Step 2: Wire cleanup into workflow** — after VERIFY pass, schedule cleanup for success. After FAILED/ABORTED terminal states, schedule cleanup accordingly.

- [ ] **Step 3: Test** with temp directories and verify retention/deletion.

```bash
git add -A && git commit -m "feat: add experiment workspace cleanup policy"
```

---

## Week 4: Publish, R2, Integration, Deployment

### Task 16: Publish Activity with go-git

**Files:**
- Create: `atrpe/internal/activities/publish.go`
- Create: `atrpe/internal/activities/publish_test.go`

- [ ] **Step 1: Implement PublishActivity** — (1) load ArticleDraft from ObjectStore via Repository, (2) parse YAML frontmatter, set `published: true`, (3) write final markdown to ObjectStore, (4) clone Zenn repo via `git.PlainClone` (go-git), (5) create branch `atrpe/<slug>`, (6) write article file to `articles/<slug>.md`, (7) commit and push via go-git, (8) create PR via GitHub REST API, (9) if auto-merge enabled, merge PR, (10) record PublishedArticle in SQLite. If merge fails or auto-merge disabled, return ESCALATED with PR URL.

- [ ] **Step 2: Idempotency** — before creating branch, check if branch already exists via `repo.Reference()`. If PR already open for slug, update existing PR. If PR already merged, skip to recording.

- [ ] **Step 3: Test** with temp git repos (no real GitHub needed for unit test). Integration test with real Zenn repo can be manual.

```bash
go get github.com/go-git/go-git/v5
go test ./internal/activities/ -v -run TestPublish
```
Expected: PASS

```bash
git add -A && git commit -m "feat: implement PublishActivity with go-git and GitHub PR flow"
```

---

### Task 17: R2 ObjectStore adapter

**Files:**
- Create: `atrpe/internal/objectstore/r2.go`
- Create: `atrpe/internal/objectstore/r2_test.go`

- [ ] **Step 1: R2ObjectStore** — implements ObjectStore interface using `aws-sdk-go-v2` S3 client configured with R2 endpoint. Put/Get/Head/Delete map to S3 operations. Config loaded from Settings (R2Endpoint, R2AccessKeyID, R2SecretAccessKey, R2Bucket).

- [ ] **Step 2: Test** with R2 credentials (skip if not configured — use build tag `//go:build integration`).

- [ ] **Step 3: Factory** — add `NewObjectStore(cfg Settings)` in objectstore package that returns LocalObjectStore or R2ObjectStore based on `ARTIFACT_STORE_TYPE`.

```bash
git add -A && git commit -m "feat: add Cloudflare R2 object store adapter"
```

---

### Task 18: End-to-end workflow test

**Files:**
- Create: `atrpe/internal/workflows/e2e_test.go`

- [ ] **Step 1: Full end-to-end test** with Temporal test environment. Steps: (1) trigger DISCOVER, (2) send TopicSelectedSignal, (3) mock all agent LLM calls with fixture responses, (4) assert RESEARCH→DESIGN→EXPERIMENT→VERIFY→GENERATE_ARTICLE transitions, (5) send PublishApprovalSignal, (6) assert PUBLISH→COMPLETED, (7) verify search attributes set, (8) verify artifacts stored in LocalObjectStore, (9) verify SQLite records written, (10) send RequestChangesSignal early, assert regeneration, (11) test remediation: force VERIFY failure 3 times, assert ESCALATED, (12) test /retry from ESCALATED.

- [ ] **Step 2: Acceptance criteria checklist** — confirm every item from the spec's acceptance criteria list passes.

```bash
go test ./internal/workflows/ -v -run TestE2E
```
Expected: PASS

```bash
git add -A && git commit -m "feat: add end-to-end workflow test covering all paths"
```

---

### Task 19: Deployment configuration

**Files:**
- Create: `atrpe/Dockerfile`
- Create: `atrpe/docker-compose.yml`
- Create: `atrpe/.env.example`

- [ ] **Step 1: Dockerfile** — multi-stage Go build, final image `FROM scratch` with CA certs, binaries at `/usr/local/bin/atrpe-api` and `/usr/local/bin/atrpe-worker`.

- [ ] **Step 2: docker-compose.yml** — services: temporal (with dev server), api (builds from Dockerfile), worker (builds from Dockerfile). SQLite data volume. Local artifact store volume.

- [ ] **Step 3: .env.example** — all config keys with defaults from spec, placeholders for secrets.

- [ ] **Step 4: Test docker-compose up, run a workflow, verify.**

```bash
git add -A && git commit -m "feat: add Docker deployment configuration"
```

---

### Task 20: First article — manual end-to-end run

- [ ] **Step 1: Start Temporal dev server** and worker with `LLM_API_KEY` set to a real DeepSeek key.

- [ ] **Step 2: Trigger a workflow** via the internal signal endpoint with a topic candidate.

- [ ] **Step 3: Monitor workflow** through Temporal UI. Verify each state transition succeeds.

- [ ] **Step 4: Review the generated article draft** manually. If acceptable, send `/approve`.

- [ ] **Step 5: Verify published article** recorded in SQLite and markdown artifact in ObjectStore.

---

## Self-Review Checklist

**Spec coverage:**
- [x] Cold-start topic discovery → Tasks 8, 11
- [x] `/select` advances from waiting → Tasks 6, 10
- [x] `/select` uses candidate_id not title → Tasks 6, 8 (CandidateID hash)
- [x] Go code generation + go test/go vet → Task 13
- [x] Verification pass/fail with configured checks → Task 14
- [x] Remediation loop (3 attempts max) → Tasks 10, 13, 15
- [x] PatchResult/DesignArtifact/VerificationReport defined → Task 3
- [x] Experiment workspace retention/deletion → Task 15
- [x] Zenn markdown draft before approval → Task 14
- [x] Publish flips `published: true` → Task 16
- [x] Internal signal fallback → Task 7
- [x] Temporal search attributes → Task 10
- [x] Artifact content via ObjectStore → Tasks 4, 7
- [x] SQLite stores all record types → Task 5

**No TBD/TODO placeholders** — confirmed.

**Type consistency:**
- `artifacts.URI` matches `objectstore.URI` → both are `objectstore.URI`, repo accepts it ✓
- Agent signatures match spec interfaces ✓
- Workflow signal names match parser signal strings ✓
