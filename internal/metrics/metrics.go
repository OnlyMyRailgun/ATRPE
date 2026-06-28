package metrics

import "github.com/prometheus/client_golang/prometheus"
import "github.com/prometheus/client_golang/prometheus/promauto"

var (
	// ArticlesGenerated counts total article drafts generated.
	ArticlesGenerated = promauto.NewCounter(prometheus.CounterOpts{
		Name: "atrpe_articles_generated_total",
		Help: "Total number of article drafts generated.",
	})

	// ArticlesPublished counts total articles published.
	ArticlesPublished = promauto.NewCounter(prometheus.CounterOpts{
		Name: "atrpe_articles_published_total",
		Help: "Total number of articles published.",
	})

	// ExperimentRuns counts experiment executions by outcome.
	ExperimentRuns = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "atrpe_experiment_runs_total",
		Help: "Total number of experiment executions.",
	}, []string{"outcome"}) // "pass", "fail"

	// RemediationLoops counts remediation loop activations.
	RemediationLoops = promauto.NewCounter(prometheus.CounterOpts{
		Name: "atrpe_remediation_loops_total",
		Help: "Total remediation loops triggered.",
	})

	// WorkflowDuration tracks workflow execution time.
	WorkflowDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "atrpe_workflow_duration_seconds",
		Help:    "Duration of article workflows.",
		Buckets: prometheus.ExponentialBuckets(60, 2, 10), // 1min to ~8.5hrs
	}, []string{"outcome"}) // "completed", "failed", "aborted"

	// LLMCallDuration tracks LLM call latency.
	LLMCallDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "atrpe_llm_call_duration_seconds",
		Help:    "Duration of LLM API calls.",
		Buckets: prometheus.DefBuckets,
	}, []string{"agent", "provider", "model"})

	// DiscoveryTopics counts topics discovered by source.
	DiscoveryTopics = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "atrpe_discovery_topics_total",
		Help: "Total topics discovered by source.",
	}, []string{"source"})
)
