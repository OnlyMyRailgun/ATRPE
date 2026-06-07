package knowledge

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	"github.com/your-org/atrpe/internal/artifacts"
	"github.com/your-org/atrpe/internal/objectstore"
	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schemaSQL string

// SQLiteStore implements KnowledgeStore using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens or creates the SQLite database and runs migrations.
func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite serialized writes

	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

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

func (s *SQLiteStore) SaveTechnicalBrief(ctx context.Context, id, topicID string, uri objectstore.URI) error {
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

// SaveArtifactMeta stores metadata about a serialized artifact.
func (s *SQLiteStore) SaveArtifactMeta(ctx context.Context, table, id, topicID string, uri objectstore.URI) error {
	query := fmt.Sprintf(`INSERT OR REPLACE INTO %s (id, topic_id, artifact_uri, created_at) VALUES (?, ?, ?, ?)`, table)
	_, err := s.db.ExecContext(ctx, query, id, topicID, string(uri), time.Now().UTC().Format(time.RFC3339))
	return err
}

// GetArtifactURI retrieves the object store URI for a previously saved artifact.
func (s *SQLiteStore) GetArtifactURI(ctx context.Context, table, id string) (objectstore.URI, error) {
	var uri string
	query := fmt.Sprintf(`SELECT artifact_uri FROM %s WHERE id = ?`, table)
	err := s.db.QueryRowContext(ctx, query, id).Scan(&uri)
	if err != nil {
		return "", fmt.Errorf("query: %w", err)
	}
	return objectstore.URI(uri), nil
}
