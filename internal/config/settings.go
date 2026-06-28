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
	GitHubToken               string
	GitHubAppID               int64
	GitHubAppPrivateKey       string
	GitHubAppInstallationID   int64
	GitHubIssueRepo           string
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
		VerificationChecks:        getEnvSlice("VERIFICATION_CHECKS", "lint,vet,tests,links,citations"),
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
		GitHubToken:               os.Getenv("GITHUB_TOKEN"),
		GitHubAppID:               getEnvInt64("GITHUB_APP_ID", 0),
		GitHubAppPrivateKey:       os.Getenv("GITHUB_APP_PRIVATE_KEY"),
		GitHubAppInstallationID:   getEnvInt64("GITHUB_APP_INSTALLATION_ID", 0),
		GitHubIssueRepo:           os.Getenv("GITHUB_ISSUE_REPO"),
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

func getEnvInt64(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
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
