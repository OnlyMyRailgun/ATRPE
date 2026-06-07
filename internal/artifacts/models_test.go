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
		Assumptions:     []string{"local execution only"},
		Constraints:     []string{"no cloud resources"},
		SuccessCriteria: []string{"tests pass", "lint clean"},
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
