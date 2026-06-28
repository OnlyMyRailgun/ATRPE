# ATRPE Phase 2: Quality & Brand Readiness Spec

## Purpose

This document defines the gaps between the current ATRPE MVP implementation and the goal of establishing the **FDE personal brand** through high-quality, code-verified technical articles on Zenn. It serves as the implementation spec for the next phase of development.

The MVP delivers a working workflow skeleton. Phase 2 must make that skeleton produce **credible, verifiable, reader-trusted technical content**—the minimum bar for building a personal brand.

## Gap Assessment Summary

| Gap | Severity | Impact on Brand |
|-----|----------|----------------|
| Experiment/Verify stubs | **Critical** | Core differentiator ("code-verified articles") is a lie |
| Simulated research (no real web fetch) | **Critical** | Articles may contain hallucinated facts and broken URLs |
| Single discovery source (GitHub `topic:llm` only) | High | Homogeneous content, no competitive moat |
| No observability (metrics / tracing / dashboards) | High | Cannot measure quality or iterate |
| Basic LLM prompts (no few-shot, no Zenn conventions) | High | Article quality below human standard |
| Zero published articles | **Critical** | No evidence of value exists |
| No reader feedback loop | Medium | No way to improve based on reception |
| No open-source README / community presence | Medium | Tool invisible to potential audience |
| No multi-language strategy (en + ja) | Medium | Zenn's primary audience is Japanese |
| Workspace cleanup not wired | Low | Disk bloat on long-running instances |

---

## 1. Experiment & Verification Pipeline Closure

### 1.1 Current State

```go
// article_workflow.go:256-262 — the critical gap
func runExperiment(ctx workflow.Context, s ArticleWorkflowState) ArticleWorkflowState {
    comment(ctx, s.IssueNumber, "🧪 Experiment skipped (MVP), proceeding to article generation...")
    s.State = StateGenerateArticle  // <-- skips everything
    return s
}
```

Both `runExperiment` and `runVerify` are no-ops. The workflow jumps directly from Design to article generation. The **code-verified article** value proposition is not delivered.

### 1.2 Required Changes

#### 1.2.1 Wire Experiment in Workflow

`runExperiment` must:
1. Call `RunExperiment` activity with the `DesignArtifact` produced by `DesignArchitecture`.
2. Store the `ExperimentResult` in workflow state for downstream use.
3. Transition to `StateVerify` on success, `StateFailed` on unrecoverable error.

#### 1.2.2 Wire Verify in Workflow

`runVerify` must:
1. Call `VerifyExperiment` activity with `TechnicalBrief` + `ExperimentResult`.
2. On `OverallPassed == true` → transition to `StateGenerateArticle`.
3. On `OverallPassed == false` → increment `RemediationCount`:
   - If `RemediationCount < MaxRemediation` → transition to `StatePatchGeneration`.
   - If `RemediationCount >= MaxRemediation` → transition to `StateEscalated`.
4. Store `VerificationReport` in workflow state.

#### 1.2.3 Wire Remediation Loop

`runPatchGeneration` must:
1. Call `RunExperiment.Patch()` activity with the current `DesignArtifact` + failed `ExperimentResult`.
2. Produce a `PatchResult` with new file hashes.
3. Transition to `StateDesignUpdate`.

`runDesignUpdate` must:
1. Call `DesignArchitecture.Update()` activity with current `DesignArtifact` + `PatchResult`.
2. Produce updated `DesignArtifact` that incorporates patch feedback.
3. Transition to `StateExperiment` for re-run.

#### 1.2.4 Experiment-Rich Article Generation

In `runGenerateArticle`, pass the full artifact chain to `GenerateDraft`:
```go
GenerateDraftInput{
    Brief:       brief,        // TechnicalBrief with research findings
    Result:      experimentResult,  // actual command outputs, not stubs
    Report:      verificationReport, // pass/fail/warnings
    ChangeNotes: s.ChangeNotes,
}
```

### 1.3 Acceptance Criteria

- [ ] A workflow that enters `EXPERIMENT` state must produce a non-empty `ExperimentResult` with at least one `CommandResult`.
- [ ] `go vet ./...` and `go test ./...` run on LLM-generated code before article generation.
- [ ] A verification failure enters `PATCH_GENERATION` → `DESIGN_UPDATE` → `EXPERIMENT` loop.
- [ ] After `MAX_REMEDIATION_ATTEMPTS` failures, the workflow enters `ESCALATED`.
- [ ] The generated article's Implementation section contains actual code snippets from the experiment, not placeholders.
- [ ] The generated article's Evaluation section references experiment command outputs (pass/fail, stdout excerpts).

---

## 2. Real Web-Backed Research

### 2.1 Current State

```go
// research.go system prompt:
"You are a technical research assistant. Given a topic, gather and synthesize
 information as if you had access to official documentation..."
```

The Research Agent **does not actually fetch web pages**. It relies entirely on the LLM's training data memory, which means:
- API signatures may be outdated (training cutoff).
- Source URLs in `SourceRef` may be hallucinated.
- Citations cannot be verified by VerificationAgent.

### 2.2 New Component: `WebFetcher`

```go
// internal/research/fetcher.go

type FetchedPage struct {
    URL         string
    Title       string
    Content     string // cleaned text, truncated to ~8K tokens
    ContentHash string // sha256[:16]
    RetrievedAt time.Time
    StatusCode  int
    Error       string // empty if successful
}

type WebFetcher interface {
    Fetch(ctx context.Context, url string) (*FetchedPage, error)
    FetchMultiple(ctx context.Context, urls []string, concurrency int) ([]*FetchedPage, error)
}
```

Implementation: `DefaultWebFetcher` using `net/http` with:
- 10s timeout per request.
- Respect `robots.txt` (optional for MVP, record as warning).
- HTML-to-text extraction (strip tags, scripts, styles — use `golang.org/x/net/html` or a simple regex-based stripper).
- Truncate to ~8000 tokens (~32KB) to fit LLM context window.
- Store fetched pages in ObjectStore keyed by `content_hash` for citation deduplication.

### 2.3 Research Agent Changes

Replace the "as if" prompt with a **two-phase** approach:

**Phase A — URL Discovery:**
```text
Given a topic and its GitHub/Zenn/HN URL, list the top 5-10 official
documentation pages, RFCs, and high-quality articles you would need to
fully understand this topic. Output a JSON array of {url, title, reason}.
```

**Phase B — Synthesis with Real Content:**
1. Fetch all discovered URLs via `WebFetcher`.
2. Construct a prompt that includes the **actual fetched content** (truncated):
   ```text
   You are a technical research assistant. Below are the actual contents
   of documentation and articles about this topic. Synthesize them into a
   TechnicalBrief.

   ## Source 1: <title> (<url>)
   <truncated content>

   ## Source 2: <title> (<url>)
   <truncated content>
   ```

3. The `Sources` field in `TechnicalBrief` must only contain URLs that were **actually fetched with HTTP 200**.

### 2.4 Citation Registry Integration

After research completes, register each fetched source:
```go
store.RegisterCitation(ctx, CitationRecord{
    SourceURL:    page.URL,
    ContentHash:  page.ContentHash,
    HashAlgorithm: "sha256",
    RetrievedAt:  page.RetrievedAt,
})
```

This enables VerificationAgent to check link liveness later.

### 2.5 Acceptance Criteria

- [ ] ResearchAgent fetches at least the topic's primary URL before synthesis.
- [ ] ResearchAgent discovers 5+ additional URLs and fetches them.
- [ ] `TechnicalBrief.Sources` contains only URLs that returned HTTP 200.
- [ ] Fetched page contents are stored in ObjectStore for audit.
- [ ] Citation registry records are created for each successful fetch.
- [ ] A fetch failure for one URL does not block the entire research phase (degraded, not failed).

---

## 3. Multi-Source Topic Discovery

### 3.1 Current State

`DiscoverAll()` iterates over configured sources but only `github_trending` has an implementation. Even GitHub discovery is narrowed to `language:go topic:llm`.

### 3.2 Required Source Implementations

#### 3.2.1 Hacker News (`hackernews`)

Use the [Hacker News API](https://github.com/HackerNews/API) (official, no key required):

```
GET https://hacker-news.firebaseio.com/v0/topstories.json       → top 500 IDs
GET https://hacker-news.firebaseio.com/v0/item/{id}.json        → item details
```

Filtering logic:
- Only items with `type: "story"` and a URL.
- Extract domain from URL, match against tech domains (github.com, gitlab.com, *.dev, medium.com, dev.to, arxiv.org).
- Score = `(score / 500) * 0.4 + recency_score(published_at) * 0.3 + title_specificity(title) * 0.3`.

Candidate schema for HN:
```go
CandidateID("hackernews", fmt.Sprintf("https://news.ycombinator.com/item?id=%d", itemID))
```

#### 3.2.2 Zenn Trending (`zenn_trending`)

Scrape or use Zenn API:
```
GET https://zenn.dev/api/articles?order=latest&count=20
```

Purpose: **Competitive gap analysis**.
- For each trending Zenn article, extract `title`, `topics` (tags), `liked_count`.
- Identify topics with high engagement but low article count → underserved niches.
- Do NOT generate articles *about* other Zenn articles. Instead, use trending topics as *signals* for what the Japanese dev community cares about right now.
- Score = `engagement_score(likes) * 0.5 + novelty_score(topics) * 0.5`.

#### 3.2.3 Qiita Trending (`qiita_trending`)

```
GET https://qiita.com/api/v2/items?page=1&per_page=20&query=stocks%3A%3E3
```

Same competitive analysis logic as Zenn. The combination of Zenn + Qiita sampling gives a view into what Japanese developers are reading.

#### 3.2.4 RSS Feeds (`rss_feeds`)

Configurable feed list via env var `RSS_FEED_URLS` (comma-separated). Defaults:
```
https://go.dev/blog/feed.atom,
https://blog.golang.org/feed.atom,
https://kubernetes.io/feed.xml
```

Parse with `encoding/xml` (Atom and RSS 2.0), extract entries, score by recency.

### 3.3 Source-Specific Scoring

Define a `CandidateInput` struct that captures source-specific signals:

```go
type CandidateInput struct {
    RepoName        string
    Description     string
    GithubStars     int
    PublishedAt     time.Time
    HackerNewsScore int      // 0 if not HN
    ZennLikes       int      // 0 if not Zenn
    QiitaStocks     int      // 0 if not Qiita
}
```

Each source has its own normalization function that maps source-native popularity to `0..1`.

### 3.4 Combined Discovery Pipeline

```
DiscoverAll(ctx, sources) → []TopicCandidate:
  1. Fan-out: run each source in parallel (goroutines with errgroup).
  2. Merge: concatenate all results.
  3. Deduplicate: same URL → keep highest score.
  4. Cross-source boost: if a topic appears in 2+ sources, multiply score by 1.2.
  5. Sort by score descending.
  6. Store all in SQLite.
  7. Return top N (configurable, default 5).
```

### 3.5 Acceptance Criteria

- [ ] `hackernews` source produces at least 5 candidates from top stories.
- [ ] `zenn_trending` source produces candidates with non-zero Zenn likes.
- [ ] `qiita_trending` source produces candidates with non-zero Qiita stocks.
- [ ] `rss_feeds` source parses at least one configured feed successfully.
- [ ] Cross-source deduplication prevents the same URL from appearing twice.
- [ ] A topic discovered by both GitHub and HN gets a score boost over single-source topics.

---

## 4. LLM Prompt Engineering Upgrade

### 4.1 Current State

Every agent uses a single system prompt with basic instructions. Key deficiencies:
- No few-shot examples.
- No Zenn-specific formatting knowledge (message cards, details/summary folds, diff code blocks, callouts).
- No audience definition.
- No quality self-check step.
- Temperature is hardcoded to `0.3` for all agents (writers should be more creative, researchers more precise).

### 4.2 Agent-Specific Prompt Templates

#### 4.2.1 Writer Agent Prompt v2

```markdown
## Role
You are a senior technical writer for Zenn (zenn.dev), a Japanese developer
platform. Your articles are known for depth, accuracy, and practical code examples.

## Audience
Japanese software engineers with 2-5 years of experience. They read English
technical content. They value running code over prose.

## Article Structure
1. **はじめに (Background)**: Why this matters NOW. 2-3 sentences max.
2. **アーキテクチャ (Architecture)**: Visual component diagram described in text.
3. **実装 (Implementation)**: Step-by-step with REAL code blocks from the experiment.
4. **評価 (Evaluation)**: What the tests and benchmarks actually showed.
5. **トラブルシューティング (Troubleshooting)**: 3+ common problems and solutions.

## Zenn Conventions
- Use `:::message` for callouts, `:::message alert` for warnings.
- Use `<details><summary>Click to expand</summary>` for long code blocks.
- Use ````diff` for code changes.
- Emoji in section headers improves readability.
- Frontmatter: emoji, type ("tech"), topics (array), published (false).

## Quality Checklist (MUST verify before output)
- [ ] Every code block is from the actual experiment, not invented.
- [ ] All command outputs match the Provided ExperimentResult.
- [ ] At least one troubleshooting item matches an actual failed command.
- [ ] No placeholder text like "[TODO]" or "add more here".

## Few-Shot Examples
[Include 2 complete Zenn articles as reference — store in config/zenn_examples/]
```

#### 4.2.2 Research Agent Prompt v2

```markdown
## Role
You are a skeptical technical researcher. Your job is to find the GROUND TRUTH
about a topic, not to summarize blog posts.

## Process
1. Identify the official source (GitHub repo README, official docs, RFC).
2. Extract API signatures, configuration formats, and behavior guarantees.
3. Find 3+ production users and note their pain points.
4. Identify what the official docs DON'T cover (the "hidden curriculum").

## Output Rules
- Every claim must cite a specific section of a fetched source.
- If a source contradicts another, note both and flag the conflict.
- Mark confidence: [CERTAIN] / [LIKELY] / [NEEDS VERIFICATION] for each claim.
- Sources list must only include URLs you actually received in the prompt.
```

#### 4.2.3 Code Generator Prompt v2

```markdown
## Role
You are a Go programmer writing a minimal, compilable example module.

## Rules
- Generate COMPLETE files: every import, every function body.
- `go.mod` must specify a real Go version and required dependencies.
- Include at least one `_test.go` file with a table-driven test.
- The module must compile with `go build ./...` and pass `go vet ./...`.
- Include a README.md with build/run instructions.
- Do NOT use `replace` directives in go.mod.
- Maximum 5 source files total (keep the example focused).

## Output Format
```json
{
  "module_name": "github.com/example/project",
  "files": [
    {"path": "go.mod", "content": "module ..."},
    {"path": "main.go", "content": "package main\n..."}
  ],
  "entrypoint": "main.go"
}
```
```

### 4.3 Per-Agent Temperature Configuration

```go
type AgentTemperatures struct {
    Research     float64 // 0.1 — factual, consistent
    Design       float64 // 0.3 — structured but creative
    CodeGen      float64 // 0.2 — precise code
    Verification float64 // 0.0 — deterministic judgment
    Writer       float64 // 0.5 — engaging prose
}
```

Environment overrides:
```
LLM_TEMP_RESEARCH=0.1
LLM_TEMP_DESIGN=0.3
LLM_TEMP_CODEGEN=0.2
LLM_TEMP_VERIFICATION=0.0
LLM_TEMP_WRITER=0.5
```

### 4.4 Zenn Article Validation

Before `CreateArticlePR`, validate the generated draft:

```go
type ZennValidator struct{}

func (v *ZennValidator) Validate(draft ArticleDraft) []ValidationError {
    var errs []ValidationError
    // 1. Frontmatter completeness (emoji, type, topics, published)
    if draft.Emoji == "" { errs = append(...) }
    if draft.Type == "" { errs = append(...) }
    if len(draft.Topics) == 0 { errs = append(...) }
    // 2. Required sections present
    requiredSections := []string{"background", "architecture", "implementation", "evaluation", "troubleshooting"}
    // 3. Code blocks count — at least 3
    // 4. No placeholder detection — grep for "[TODO]", "[WIP]", "TBD", "Lorem ipsum"
    // 5. Link format validation
    return errs
}
```

### 4.5 Acceptance Criteria

- [ ] Writer prompt includes Zenn-specific conventions (message cards, details folds).
- [ ] Each agent has a distinct temperature based on its role.
- [ ] CodeGen prompt produces go.mod with real dependencies.
- [ ] ZennValidator catches missing frontmatter fields.
- [ ] ZennValidator catches placeholder text in article body.
- [ ] At least one few-shot example article is stored and referenced in the Writer prompt.

---

## 5. Observability & Metrics

### 5.1 Current State

- `setState()` is a no-op (`// Search attributes deferred`).
- No metrics are emitted anywhere.
- No tracing exists.
- Logs are unstructured beyond the Temporal logger bridge.

### 5.2 Temporal Search Attributes

Register the following custom search attributes in the Temporal namespace:

```
topic_id        = Text
workflow_state  = Keyword
article_slug    = Text
platform        = Keyword
```

In `setState()`:
```go
func setState(ctx workflow.Context, state WorkflowState) {
    _ = workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
        "workflow_state": string(state),
    })
}
```

After topic selection:
```go
_ = workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
    "topic_id": s.CandidateID,
})
```

After article generation:
```go
_ = workflow.UpsertSearchAttributes(ctx, map[string]interface{}{
    "article_slug": draft.Slug,
    "platform":     "zenn",
})
```

### 5.3 Prometheus Metrics

Add `internal/metrics/metrics.go`:

```go
package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
    ArticlesGenerated = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "atrpe_articles_generated_total",
        Help: "Total number of article drafts generated.",
    })
    ArticlesPublished = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "atrpe_articles_published_total",
        Help: "Total number of articles published.",
    })
    ExperimentRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "atrpe_experiment_runs_total",
        Help: "Total number of experiment executions.",
    }, []string{"outcome"}) // "pass", "fail"
    RemediationLoops = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "atrpe_remediation_loops_total",
        Help: "Total remediation loops triggered.",
    })
    WorkflowDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
        Name:    "atrpe_workflow_duration_seconds",
        Help:    "Duration of article workflows.",
        Buckets: prometheus.ExponentialBuckets(60, 2, 10), // 1min to ~8.5hrs
    }, []string{"outcome"}) // "completed", "failed", "aborted"
    LLMCallDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
        Name:    "atrpe_llm_call_duration_seconds",
        Help:    "Duration of LLM API calls.",
        Buckets: prometheus.DefBuckets,
    }, []string{"agent", "provider", "model"})
    LLMCallTokens = prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "atrpe_llm_tokens_total",
        Help: "Total tokens consumed by LLM calls.",
    }, []string{"agent", "provider", "direction"}) // "input" | "output"
)
```

Expose via `/metrics` endpoint on `cmd/api`:
```go
http.Handle("/metrics", promhttp.Handler())
```

### 5.4 Workflow Metrics Instrumentation

In `ArticleWorkflow`, track duration:
```go
func ArticleWorkflow(ctx workflow.Context, input ArticleWorkflowInput) error {
    startTime := workflow.Now(ctx)
    defer func() {
        duration := workflow.Now(ctx).Sub(startTime)
        metrics.WorkflowDuration.WithLabelValues(outcome).Observe(duration.Seconds())
    }()
    // ...
}
```

In `LLMClient.ChatWithMaxTokens`:
```go
func (c *LLMClient) ChatWithMaxTokens(...) (string, error) {
    start := time.Now()
    defer func() {
        metrics.LLMCallDuration.WithLabelValues(agentName, c.config.Provider, c.config.Model).
            Observe(time.Since(start).Seconds())
    }()
    // ...
}
```

### 5.5 Health Check Endpoint

`cmd/api`:
```go
// GET /health
// Returns 200 if Temporal connection is alive and database is reachable.
// Response: {"status":"ok","temporal":true,"sqlite":true,"uptime_seconds":3600}
```

### 5.6 Acceptance Criteria

- [ ] Temporal search attributes are registered and set for every workflow execution.
- [ ] `/metrics` endpoint exposes Prometheus metrics on `cmd/api`.
- [ ] `atrpe_llm_call_duration_seconds` histogram records per-agent timing.
- [ ] `atrpe_articles_generated_total` counter increments on each `GenerateDraft`.
- [ ] `atrpe_articles_published_total` counter increments on each successful `PublishArticle`.
- [ ] Timeline queries in Temporal Web show workflow state transitions.
- [ ] `/health` endpoint returns 200 when all dependencies are healthy.

---

## 6. Article Quality Feedback Loop

### 6.1 The Missing Ingredient

A personal brand grows when content **improves over time based on reader response**. Currently ATRPE has no mechanism to capture or learn from feedback.

### 6.2 Engagement Tracking

Extend `engagement_metrics` table:
```sql
ALTER TABLE engagement_metrics ADD COLUMN comments INTEGER DEFAULT 0;
ALTER TABLE engagement_metrics ADD COLUMN bookmarks INTEGER DEFAULT 0;
ALTER TABLE engagement_metrics ADD COLUMN article_url TEXT;
```

Create a periodic activity `CollectEngagementMetrics` that:
1. For each published article, query Zenn API: `GET https://zenn.dev/api/articles/{slug}`.
2. Parse `liked_count`, `comments_count`, `bookmarked_count`, `published_at`.
3. Update `engagement_metrics` in SQLite.

Run as a **Temporal Schedule** (e.g., daily at 09:00 JST):
```go
// cmd/scheduler/main.go
schedule := client.ScheduleOptions{
    ID:   "engagement-collector",
    Spec: client.ScheduleSpec{CronExpressions: []string{"0 9 * * *"}},
    Action: &client.ScheduleWorkflowAction{
        Workflow: workflows.EngagementCollectionWorkflow,
    },
}
```

### 6.3 Topic Quality Retrospective

After 30 days, evaluate each article's performance:
```
engagement_score = likes * 1.0 + bookmarks * 2.0 + comments * 1.5
```

Tags associated with high `engagement_score` articles get higher weight in future discovery. Topics with zero engagement after 30 days are flagged as `low_performance` and their source/tag patterns are deprioritized.

### 6.4 Article Quality Self-Assessment

After each publish, the Writer Agent should self-assess against a rubric:

```markdown
## Article Quality Rubric (rate 1-5)
1. **Depth**: Does the article go beyond surface-level explanation?
2. **Code Quality**: Are code examples minimal, correct, and runnable?
3. **Structure**: Do sections flow logically?
4. **Novelty**: Is this perspective different from existing articles?
5. **Actionability**: Can the reader apply this immediately?
```

Store the self-assessment alongside the published article for trend analysis.

### 6.5 Acceptance Criteria

- [ ] Daily engagement collection workflow runs on a Temporal Schedule.
- [ ] `engagement_metrics` table is updated with Zenn API data.
- [ ] Articles older than 30 days have a computed `engagement_score`.
- [ ] Topic discovery weighting incorporates historical engagement data.
- [ ] A dashboard or query exists showing top-performing and lowest-performing articles.

---

## 7. Open Source & Community Presence

### 7.1 Current State

- Module path is `github.com/your-org/atrpe` (placeholder).
- No README.
- No license.
- No CONTRIBUTING guide.
- No public repository.

### 7.2 Repository Setup

```
https://github.com/<fde-username>/atrpe
```

Required files:

#### README.md (English)
```markdown
# ATRPE — Automated Technical Article Pipeline

ATRPE is a Temporal-based workflow system that produces **code-verified**
technical articles for Zenn (zenn.dev).

## What makes it different?

- 🧪 **Code runs before publication** — every code block in every article
  is generated, compiled, tested, and linted by a real Go toolchain.
- 🔍 **Multi-source discovery** — topics sourced from GitHub trending,
  Hacker News, Zenn, Qiita, and RSS feeds.
- ✍️ **Human-in-the-loop** — every article passes through a human approval
  gate before publication. You control what ships.
- 📊 **Feedback-driven** — article engagement metrics feed back into
  topic discovery, continuously improving relevance.

## Architecture

[Diagram: Temporal → Workflows → Activities → Agents → LLM]

## Quick Start

docker compose up -d    # Temporal + PostgreSQL
cp .env.example .env    # edit with your keys
go run ./cmd/pipeline   # run a full pipeline

## License

MIT
```

#### README.ja.md (Japanese)
Same content, translated for Zenn's primary audience.

### 7.3 Open Source Checklists

- [ ] LICENSE file (MIT).
- [ ] CODEOWNERS file.
- [ ] Issue templates (`.github/ISSUE_TEMPLATE/`).
- [ ] PR template (`.github/PULL_REQUEST_TEMPLATE.md`).
- [ ] CI badge in README (GitHub Actions).
- [ ] Go Report Card badge.

### 7.4 Content Strategy

Publish at minimum these foundational articles on Zenn:

1. **「コード実証済み」技術記事を自動生成する仕組み** — ATRPE overview in Japanese.
2. **How I Built an AI Article Pipeline with Temporal and Go** — English deep-dive.
3. **Why Your AI-Generated Code Examples Are Broken (And How to Fix Them)** — Problem/solution framing.
4. **Designing LLM Agents That Verify Before They Write** — Agent architecture walkthrough.

Each article should:
- Reference ATRPE as the tool that produced it.
- Include a "How this article was created" appendix showing verification evidence.
- Link to the GitHub repository.

### 7.5 Acceptance Criteria

- [ ] Repository is public at `github.com/<user>/atrpe`.
- [ ] Module path is updated from `github.com/your-org/atrpe` to actual repo.
- [ ] README.md in English and README.ja.md in Japanese exist.
- [ ] MIT LICENSE file committed.
- [ ] GitHub Actions CI runs `go test ./...` and `golangci-lint run` on PRs.
- [ ] At least one article on Zenn links back to the repository.

---

## 8. Deployment & Operations Polish

### 8.1 Current State

Docker Compose exists with Temporal + PostgreSQL. Worker runs as a single process. No production deployment guide.

### 8.2 Production Readiness Checklist

- [ ] **Graceful shutdown**: Worker handles SIGTERM → stop accepting new tasks → drain in-flight → exit. (Mostly implemented, verify.)
- [ ] **Health checks**: `/health` endpoint on API server. Docker HEALTHCHECK in Dockerfile.
- [ ] **Litestream sidecar**: SQLite WAL replication to R2 for disaster recovery.
- [ ] **Log levels**: Respect `LOG_LEVEL` env var (debug/info/warn/error).
- [ ] **Rate limiting**: LLM API calls should have configurable max concurrent calls per provider.
- [ ] **Workspace cleanup**: Wire the cleanup policy from spec (24h retention on success, 72h on failure, immediate on abort).
- [ ] **Secrets management**: Document that `LLM_API_KEY`, `GITHUB_APP_PRIVATE_KEY`, and `R2_SECRET_ACCESS_KEY` must not be logged.

### 8.3 Workspace Cleanup Implementation

```go
// internal/activities/cleanup.go
type CleanupWorkspaceInput struct {
    Workdir     string `json:"workdir"`
    Outcome     string `json:"outcome"` // "success", "failure", "abort"
}

func (a *Activities) CleanupWorkspace(ctx context.Context, input CleanupWorkspaceInput) error {
    info, err := os.Stat(input.Workdir)
    if os.IsNotExist(err) {
        return nil // already cleaned
    }

    retention := 24 * time.Hour // success default
    if input.Outcome == "failure" {
        retention = 72 * time.Hour
    } else if input.Outcome == "abort" {
        return os.RemoveAll(input.Workdir) // immediate
    }

    if time.Since(info.ModTime()) > retention {
        return os.RemoveAll(input.Workdir)
    }
    return nil
}
```

Schedule cleanup via a periodic Temporal workflow or cron.

### 8.4 Acceptance Criteria

- [ ] Worker shuts down gracefully within 30s of SIGTERM.
- [ ] Docker HEALTHCHECK returns healthy only when Temporal is connected.
- [ ] Log output level is configurable without rebuild.
- [ ] Successful experiment workspaces are deleted after 24h.
- [ ] Failed experiment workspaces are retained for 72h.

---

## 9. Multi-Language Strategy

### 9.1 Rationale

Zenn's primary reader base is Japanese-speaking. Articles published only in English miss the platform's core audience. However, English articles reach the global Go community and establish broader credibility.

### 9.2 Strategy: Bilingual by Default

For each topic:
1. Generate article in **Japanese** as `articles/{slug}.md` (primary — matches Zenn audience).
2. Generate article in **English** as `articles/{slug}.en.md` (secondary — for global reach).
3. Both go into the same PR, same Zenn publication (Zenn supports multi-language via `published` frontmatter).

### 9.3 Implementation

WriterAgent accepts a `language` parameter:
```go
type WriterAgent struct {
    llm      *LLMClient
    language string // "ja" or "en"
}
```

Generate both variants in `runGenerateArticle`:
```go
jaDraft := writerJapanese.Run(ctx, brief, result, report, changeNotes)
enDraft := writerEnglish.Run(ctx, brief, result, report, changeNotes)
```

Frontmatter for Japanese article:
```yaml
---
title: "Goで始める〇〇入門"
emoji: "🚀"
type: "tech"
topics: ["go", "llm"]
published: false
lang: "ja"
---
```

### 9.4 Acceptance Criteria

- [ ] `DEFAULT_ARTICLE_LANGUAGE` config defaults to `"ja"`.
- [ ] WriterAgent can produce articles in both `ja` and `en`.
- [ ] Both language variants are created in the same PR.
- [ ] Japanese articles use appropriate technical terminology (外来語 where standard, otherwise 和訳).

---

## 10. Implementation Phases & Effort Estimate

### Phase 2A: Pipeline Integrity (2 weeks)

| # | Task | Effort | Depends On |
|---|------|--------|------------|
| 1 | Wire Experiment activity into workflow | 1 day | — |
| 2 | Wire Verify activity into workflow | 0.5 day | 1 |
| 3 | Wire remediation loop (Patch → DesignUpdate → Experiment) | 1 day | 1, 2 |
| 4 | Pass full artifact chain to GenerateDraft | 0.5 day | 1, 2 |
| 5 | End-to-end test: code runs → article includes real command outputs | 1 day | 1-4 |
| 6 | Research Agent WebFetcher component | 2 days | — |
| 7 | Research Agent two-phase prompt with real content | 1 day | 6 |
| 8 | HN / Zenn / Qiita / RSS discovery sources | 3 days | — |

### Phase 2B: Quality & Polish (2 weeks)

| # | Task | Effort | Depends On |
|---|------|--------|------------|
| 9 | Writer prompt v2 (Zenn conventions, few-shot, quality checklist) | 1 day | — |
| 10 | Per-agent temperatures | 0.5 day | — |
| 11 | Zenn article validator | 1 day | 9 |
| 12 | Temporal search attributes registration | 0.5 day | — |
| 13 | Prometheus metrics + `/metrics` endpoint | 1.5 days | — |
| 14 | Health check endpoint | 0.5 day | — |
| 15 | Workspace cleanup cron | 1 day | — |
| 16 | Engagement metrics collection schedule | 1.5 days | — |
| 17 | Produce first Japanese article with full pipeline | 1 day | all above |

### Phase 2C: Brand & Community (1-2 weeks)

| # | Task | Effort | Depends On |
|---|------|--------|------------|
| 18 | Update module path to real GitHub repo | 0.5 day | — |
| 19 | README.md (EN) + README.ja.md (JA) | 1 day | — |
| 20 | GitHub CI (go test + golangci-lint) | 0.5 day | — |
| 21 | Bilingual article generation | 1 day | 9 |
| 22 | Write & publish "How ATRPE Works" article (JA) | 2 days | first article produced |
| 23 | Write & publish "How ATRPE Works" article (EN) | 1 day | 22 |
| 24 | Deploy to a VPS or Fly.io for continuous operation | 1 day | — |

**Total estimated effort: ~5-6 weeks** for a single developer working part-time.

---

## 11. Success Metrics for Phase 2

### Quantitative

| Metric | Baseline | Phase 2 Target |
|--------|----------|---------------|
| Published Zenn articles | 0 | 5+ |
| Code verification pass rate | 0% (skipped) | >80% (after remediation) |
| Articles with real fetched sources | 0% | 100% |
| Discovery sources active | 1 of 5 | 5 of 5 |
| Metrics exported | 0 | All 7 counters/histograms |
| Language coverage | EN only | JA + EN per article |
| Repository stars | 0 | 20+ (organic) |

### Qualitative

- [ ] A reader can clone the experiment repo from an article and run `go test ./...` successfully.
- [ ] At least one article receives a Zenn "liked" count > 20 within 30 days.
- [ ] A developer unfamiliar with ATRPE can understand its purpose from the README in < 2 minutes.
- [ ] The Temporal workflow UI shows a clear, debuggable trace of each article's production.

---

## 12. Key Design Decisions (Reaffirmed & New)

1. **Temporal remains the orchestration backbone.** No migration to lighter queues. The durability and visibility Temporal provides is the architectural moat.
2. **Go remains the implementation and experiment language.** Python runner deferred to Phase 3.
3. **DeepSeek remains the default LLM provider.** Cost-effectiveness matters for a solo developer. Provider swap is a config change, not a code change.
4. **Japanese-first content strategy.** Zenn's audience is Japanese. English articles are secondary but produced alongside each Japanese article.
5. **Metrics before ML.** Observability and feedback loops are built with simple counters and histograms before any machine learning is introduced.
6. **Every article is a product.** The bar is not "can ATRPE generate text?" but "would a reader bookmark this?"
