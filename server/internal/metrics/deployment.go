package metrics

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// DeploymentMetrics contains bounded-cardinality telemetry for the generic
// deployment contract. It deliberately has no workspace, target, URL, payload,
// credential, or provider-error labels.
type DeploymentMetrics struct {
	connectionValidation     *prometheus.CounterVec
	connectionValidationTime *prometheus.HistogramVec
	providerRequest          *prometheus.CounterVec
	providerRequestTime      *prometheus.HistogramVec
	trigger                  *prometheus.CounterVec
	operationState           *prometheus.CounterVec
	webhook                  *prometheus.CounterVec
	reconciliation           *prometheus.CounterVec
	operationLongRunning     *prometheus.CounterVec
	rateLimit                *prometheus.CounterVec
}

func NewDeploymentMetrics() *DeploymentMetrics {
	return &DeploymentMetrics{
		connectionValidation: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_deployment_connection_validation_total",
			Help: "Deployment connection validation attempts by provider and bounded result.",
		}, metricLabels("multica_deployment_connection_validation_total")),
		connectionValidationTime: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "multica_deployment_connection_validation_duration_seconds",
			Help: "Deployment connection validation duration by provider and bounded result.",
		}, metricLabels("multica_deployment_connection_validation_duration_seconds")),
		providerRequest: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_deployment_provider_request_total",
			Help: "Outbound deployment provider requests by provider, operation, and bounded result.",
		}, metricLabels("multica_deployment_provider_request_total")),
		providerRequestTime: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "multica_deployment_provider_request_duration_seconds",
			Help: "Outbound deployment provider request duration by provider, operation, and bounded result.",
		}, metricLabels("multica_deployment_provider_request_duration_seconds")),
		trigger: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_deployment_trigger_total",
			Help: "Deployment trigger outcomes by provider.",
		}, metricLabels("multica_deployment_trigger_total")),
		operationState: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_deployment_operation_state_total",
			Help: "Deployment operation state observations by provider.",
		}, metricLabels("multica_deployment_operation_state_total")),
		webhook: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_deployment_webhook_total",
			Help: "Deployment webhook outcomes by provider.",
		}, metricLabels("multica_deployment_webhook_total")),
		reconciliation: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_deployment_reconciliation_total",
			Help: "Deployment reconciliation outcomes by bounded provider scope.",
		}, metricLabels("multica_deployment_reconciliation_total")),
		operationLongRunning: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_deployment_operation_long_running_total",
			Help: "Deployment operations that exceeded the reconciliation deadline.",
		}, metricLabels("multica_deployment_operation_long_running_total")),
		rateLimit: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_deployment_rate_limit_total",
			Help: "Deployment provider rate-limit responses by provider and operation.",
		}, metricLabels("multica_deployment_rate_limit_total")),
	}
}

func (m *DeploymentMetrics) Collectors() []prometheus.Collector {
	if m == nil {
		return nil
	}
	return []prometheus.Collector{
		m.connectionValidation,
		m.connectionValidationTime,
		m.providerRequest,
		m.providerRequestTime,
		m.trigger,
		m.operationState,
		m.webhook,
		m.reconciliation,
		m.operationLongRunning,
		m.rateLimit,
	}
}

func (m *DeploymentMetrics) RecordConnectionValidation(provider, result string, duration time.Duration) {
	if m == nil {
		return
	}
	provider = normalizeDeploymentProvider(provider)
	result = normalizeDeploymentResult(result)
	m.connectionValidation.WithLabelValues(provider, result).Inc()
	m.connectionValidationTime.WithLabelValues(provider, result).Observe(duration.Seconds())
}

func (m *DeploymentMetrics) RecordProviderRequest(provider, operation, result string, duration time.Duration) {
	if m == nil {
		return
	}
	provider = normalizeDeploymentProvider(provider)
	operation = normalizeDeploymentOperation(operation)
	result = normalizeDeploymentResult(result)
	m.providerRequest.WithLabelValues(provider, operation, result).Inc()
	m.providerRequestTime.WithLabelValues(provider, operation, result).Observe(duration.Seconds())
}

func (m *DeploymentMetrics) RecordTrigger(provider, result string) {
	if m != nil {
		m.trigger.WithLabelValues(normalizeDeploymentProvider(provider), normalizeDeploymentResult(result)).Inc()
	}
}

func (m *DeploymentMetrics) RecordOperationState(provider, state string) {
	if m != nil {
		m.operationState.WithLabelValues(normalizeDeploymentProvider(provider), normalizeDeploymentState(state)).Inc()
	}
}

func (m *DeploymentMetrics) RecordWebhook(provider, result string) {
	if m != nil {
		m.webhook.WithLabelValues(normalizeDeploymentProvider(provider), normalizeDeploymentResult(result)).Inc()
	}
}

func (m *DeploymentMetrics) RecordReconciliation(provider, result string) {
	if m != nil {
		m.reconciliation.WithLabelValues(normalizeDeploymentProvider(provider), normalizeDeploymentResult(result)).Inc()
	}
}

func (m *DeploymentMetrics) RecordLongRunning(provider, state string) {
	if m != nil {
		m.operationLongRunning.WithLabelValues(normalizeDeploymentProvider(provider), normalizeDeploymentState(state)).Inc()
	}
}

func (m *DeploymentMetrics) RecordRateLimit(provider, operation string) {
	if m != nil {
		m.rateLimit.WithLabelValues(normalizeDeploymentProvider(provider), normalizeDeploymentOperation(operation)).Inc()
	}
}

func normalizeDeploymentProvider(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "vercel", "fake", "render", "railway", "heroku", "all":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func normalizeDeploymentOperation(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "validate_connection", "list_targets", "get_target", "create_target", "delete_target", "trigger", "get_operation", "webhook":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func normalizeDeploymentResult(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "success", "error", "rate_limited", "verified", "verification_failed", "duplicate", "unmatched", "applied", "ignored":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func normalizeDeploymentState(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pending", "queued", "dispatching", "running", "building", "deploying", "ready", "succeeded", "failed", "canceled", "cancelled", "retry_wait", "deadline":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}
