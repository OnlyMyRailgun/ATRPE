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
