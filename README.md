# ATRPE — Automated Technical Research & Publishing Engine

ATRPE is a [Temporal](https://temporal.io)-based workflow system that produces **code-verified** technical articles for [Zenn](https://zenn.dev).

## What Makes It Different?

- 🧪 **Code runs before publication** — every code block in every article is generated, compiled, tested, and linted by a real Go toolchain.
- 🔍 **Multi-source discovery** — topics sourced from GitHub trending, Hacker News, Zenn, Qiita, and RSS feeds.
- ✍️ **Human-in-the-loop** — every article passes through a human approval gate before publication. You control what ships.
- 🔄 **Self-healing** — failed experiments trigger automatic remediation (patch → design update → re-run) up to 3 times before escalation.
- 📊 **Feedback-driven** — article engagement metrics feed back into topic discovery, continuously improving relevance.

## Architecture

```
GitHub Issue (topic selection)
        │
        ▼
   Temporal Workflow
        │
   ┌────┼────────────────────────────┐
   ▼    ▼         ▼         ▼       ▼
Discover → Research → Design → Experiment → Verify
                                           │
                              ┌────────────┼────────────┐
                              ▼            ▼            ▼
                        GenerateArticle  PatchGen → DesignUpdate
                              │
                              ▼
                     Human Approval (GitHub comments)
                              │
                              ▼
                           Publish
```

- **Temporal** orchestrates durable, long-running workflows with human approval gates.
- **Go modules** implement all system logic (agents, activities, storage).
- **SQLite** stores topic candidates, artifact metadata, and engagement metrics.
- **Cloudflare R2** (or local disk) stores full artifact content.
- **DeepSeek API** (OpenAI-compatible) powers all LLM agents.

## Quick Start

```bash
# 1. Start Temporal + PostgreSQL
docker compose up -d

# 2. Configure
cp .env.example .env
# Edit .env with your LLM_API_KEY and GitHub credentials

# 3. Run a full pipeline
go run ./cmd/pipeline

# 4. Or start the worker (processes GitHub issue commands)
go run ./cmd/worker
```

## Configuration

| Env Var | Default | Description |
|----------|---------|-------------|
| `LLM_API_KEY` | *required* | API key for DeepSeek (or other provider) |
| `LLM_PROVIDER` | `deepseek` | LLM provider name |
| `LLM_MODEL` | `deepseek-chat` | Model ID |
| `LLM_BASE_URL` | `https://api.deepseek.com/v1` | API base URL |
| `TEMPORAL_HOST_PORT` | `localhost:7233` | Temporal server address |
| `DEFAULT_ARTICLE_LANGUAGE` | `ja` | Primary article language (`ja` or `en`) |
| `BILINGUAL_ARTICLES` | `false` | Generate both JA and EN variants |
| `TOPIC_SOURCES` | `github_trending,hackernews,zenn_trending,qiita_trending,rss_feeds` | Discovery sources |
| `MAX_REMEDIATION_ATTEMPTS` | `3` | Max fix attempts before escalation |
| `GITHUB_ISSUE_REPO` | — | GitHub repo for issue creation |

See `.env.example` for the full list.

## Development

```bash
# Run tests
go test ./...

# Run linter
golangci-lint run

# Build
go build ./cmd/...
```

## License

MIT

---

🤖 *ATRPE articles are generated and verified by ATRPE itself.*
