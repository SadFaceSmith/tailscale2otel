package app

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v5/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/provider"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestK8sHealthConfiguredTargetEmitsReadiness(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" || r.Header.Get("Authorization") != "Bearer fixture-token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	dir := t.TempDir()
	ca, token := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "token")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Collectors.K8sHealth.Enabled = true
	cfg.Collectors.K8sHealth.Targets = []config.K8sHealthTarget{{Cluster: "cluster", URL: srv.URL, CAFile: ca, BearerTokenFile: token}}
	rec := telemetrytest.New()
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", rec)
	for _, entry := range a.runtimes[0].registry.Entries() {
		if entry.Collector.Name() != "k8s_health" {
			continue
		}
		if err := entry.Collector.(collector.SnapshotCollector).Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatal(err)
		}
		points := rec.MetricPoints("tailscale.k8s.health.success")
		if len(points) != 1 || points[0].Value != 1 || points[0].Attrs["k8s.cluster.name"] != "cluster" {
			t.Fatalf("readiness: %+v", points)
		}
		return
	}
	t.Fatal("collector not registered")
}

func TestK8sHealthRegistration(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := config.Default()
		cfg.Collectors.K8sHealth.Enabled = enabled
		cfg.Collectors.K8sHealth.Targets = []config.K8sHealthTarget{{Cluster: "cluster", URL: "https://127.0.0.1:1"}}
		rec := telemetrytest.New()
		a := newAppShell(cfg, "vtest", nil, rec.Emitter(), tracenoop.NewTracerProvider().Tracer("test"),
			func(context.Context) error { return nil }, collector.NewMemoryStore())
		a.buildProcessDeps()
		for _, name := range []string{"alpha.example.com", "beta.example.com"} {
			a.addRuntime(name, rec.Emitter(), nil, nil, provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), true)
		}
		if got := runtimeHasCollector(a.runtimes[0], "k8s_health"); got != enabled {
			t.Fatalf("primary registered=%v, enabled=%v", got, enabled)
		}
		if runtimeHasCollector(a.runtimes[1], "k8s_health") {
			t.Fatal("static cluster targets registered twice")
		}
	}
}

func TestK8sHealthFailureDoesNotGateExporterReadiness(t *testing.T) {
	failed := readyRan("k8s_health", false)
	failed.ConsecutiveFailures = 5
	if ready, reason := readinessVerdict([]statusdata.CollectorStatus{failed}, nil); !ready {
		t.Fatalf("remote cluster failure made exporter unready: %s", reason)
	}
}
