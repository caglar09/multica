package metrics

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDeploymentMetricsHappyPath(t *testing.T) {
	m := NewDeploymentMetrics()
	m.RecordProviderRequest("vercel", "trigger", "success", 25*time.Millisecond)
	m.RecordTrigger("vercel", "success")
	m.RecordOperationState("vercel", "queued")

	if got := testutil.ToFloat64(m.providerRequest.WithLabelValues("vercel", "trigger", "success")); got != 1 {
		t.Fatalf("provider request count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.trigger.WithLabelValues("vercel", "success")); got != 1 {
		t.Fatalf("trigger count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.operationState.WithLabelValues("vercel", "queued")); got != 1 {
		t.Fatalf("operation state count = %v, want 1", got)
	}
}

func TestDeploymentMetricsRedactsUnboundedLabels(t *testing.T) {
	m := NewDeploymentMetrics()
	m.RecordProviderRequest(
		"https://vercel.example/api?token=secret",
		"/v13/deployments?payload=secret",
		`{"token":"secret"}`,
		time.Second,
	)

	if got := testutil.ToFloat64(m.providerRequest.WithLabelValues("other", "other", "other")); got != 1 {
		t.Fatalf("redacted provider request count = %v, want 1", got)
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(m.Collectors()...)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	if strings.Contains(fmt.Sprint(families), "secret") {
		t.Fatal("deployment metric output contains an unbounded secret value")
	}
}
