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
