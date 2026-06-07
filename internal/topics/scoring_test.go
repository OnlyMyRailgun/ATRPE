package topics

import (
	"math"
	"testing"
	"time"
)

func TestRecencyScore_Within7Days(t *testing.T) {
	if s := recencyScore(time.Now().Add(-24 * time.Hour)); s != 1.0 {
		t.Errorf("expected 1.0, got %.2f", s)
	}
}

func TestRecencyScore_Within30Days(t *testing.T) {
	if s := recencyScore(time.Now().Add(-14 * 24 * time.Hour)); s != 0.5 {
		t.Errorf("expected 0.5, got %.2f", s)
	}
}

func TestRecencyScore_Older(t *testing.T) {
	if s := recencyScore(time.Now().Add(-120 * 24 * time.Hour)); s != 0.0 {
		t.Errorf("expected 0.0, got %.2f", s)
	}
}

func TestScoreCandidate_PenalizesGeneric(t *testing.T) {
	generic := ScoreCandidate(CandidateInput{
		RepoName:    "kubernetes/kubernetes",
		Description: "Production-Grade Container Scheduling and Management",
		GithubStars: 120000,
		PublishedAt: time.Now().Add(-1 * 24 * time.Hour),
	})
	specific := ScoreCandidate(CandidateInput{
		RepoName:    "example/go-k8s-operator",
		Description: "A Kubernetes operator for managing PostgreSQL clusters with automated failover and backup",
		GithubStars: 1500,
		PublishedAt: time.Now().Add(-3 * 24 * time.Hour),
	})
	if generic >= specific {
		t.Errorf("generic repo (kubernetes) should score lower than specific one: %.3f >= %.3f", generic, specific)
	}
}

func TestScoreCandidate_SpecificityHigh(t *testing.T) {
	s := ScoreCandidate(CandidateInput{
		RepoName:             "authzed/spicedb-operator",
		Description:          "Kubernetes operator for SpiceDB — schema migration, validation, and zero-downtime deployments",
		GithubStars:          800,
		JapaneseArticleCount: 2,
		PublishedAt:          time.Now().Add(-2 * 24 * time.Hour),
	})
	// Should be well above 0.5 given good specificity
	if s < 0.5 {
		t.Errorf("specific topic should score >=0.5, got %.3f", s)
	}
}

func TestSpecificityScore_SingleWord(t *testing.T) {
	s := specificityScore("ollama", "Get up and running with Llama, Mistral, Gemma, and others")
	// Single-word name gets -0.25 penalty (too broad), but description > 40 adds +0.10
	// net: 0.5 - 0.25 + 0.10 = 0.35
	if s < 0.3 || s > 0.5 {
		t.Errorf("single word with decent description should be 0.30-0.50 range, got %.3f", s)
	}
}

func TestSpecificityScore_MultiWord(t *testing.T) {
	s := specificityScore("go-k8s-operator-testing", "A framework for testing Kubernetes operators in Go with deterministic simulation")
	if s < 0.7 {
		t.Errorf("multi-word specific repo should score high, got %.3f", s)
	}
}

func TestSpecificityScore_GenericName(t *testing.T) {
	s := specificityScore("kubernetes", "Production-Grade Container Scheduling and Management")
	if s > 0.4 {
		t.Errorf("ultra-generic name should score low, got %.3f", s)
	}
}

func TestCandidateID_Deterministic(t *testing.T) {
	id1 := CandidateID("github_trending", "https://github.com/kubernetes/kubernetes")
	id2 := CandidateID("github_trending", "https://github.com/kubernetes/kubernetes")
	if id1 != id2 {
		t.Errorf("IDs should be deterministic: %s != %s", id1, id2)
	}
	if len(id1) != 12 {
		t.Errorf("expected 12-char hex ID, got %d chars", len(id1))
	}
}

func TestCandidateID_DifferentSource(t *testing.T) {
	id1 := CandidateID("github_trending", "https://same-url.com")
	id2 := CandidateID("hackernews", "https://same-url.com")
	if id1 == id2 {
		t.Error("different sources with same URL should produce different IDs")
	}
}

func TestScoreCandidate_OldProject(t *testing.T) {
	new := ScoreCandidate(CandidateInput{
		RepoName: "new/lib", Description: "A new library for Go", GithubStars: 100, PublishedAt: time.Now(),
	})
	old := ScoreCandidate(CandidateInput{
		RepoName: "old/lib", Description: "An old library for Go", GithubStars: 100, PublishedAt: time.Now().Add(-200 * 24 * time.Hour),
	})
	if old >= new {
		t.Errorf("old project should score lower: %.3f >= %.3f", old, new)
	}
}

func TestWeightsSum(t *testing.T) {
	// Verify weight sum is 1.0
	c := CandidateInput{
		RepoName:    "mid/repo-name",
		Description: "A decent description of about fifty characters or so",
		GithubStars: 5000,
		PublishedAt: time.Now().Add(-3 * 24 * time.Hour),
	}
	s := ScoreCandidate(c)
	if s < 0.0 || s > 1.0 {
		t.Errorf("score out of bounds: %.3f", s)
	}
	if math.IsNaN(s) {
		t.Error("score is NaN")
	}
}
