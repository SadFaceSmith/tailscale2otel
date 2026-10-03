package k8shealth

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/apistate"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func trustedTarget(t *testing.T, srv *httptest.Server) Target {
	t.Helper()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	return Target{Cluster: "test-cluster", URL: srv.URL, CAFile: ca}
}

func point(t *testing.T, rec *telemetrytest.Recorder, name string) telemetrytest.MetricPoint {
	t.Helper()
	points := rec.MetricPoints(name)
	if name == docOutcome.Name {
		active := points[:0]
		for _, p := range points {
			if p.Value == 1 {
				active = append(active, p)
			}
		}
		points = active
	}
	if len(points) != 1 {
		t.Fatalf("%s: got %d points: %+v", name, len(points), points)
	}
	return points[0]
}

func TestReadinessAndRecovery(t *testing.T) {
	var status atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" || r.Method != http.MethodGet {
			t.Errorf("request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Location", "/unexpected")
		w.WriteHeader(int(status.Load()))
	}))
	defer srv.Close()
	c := New(Options{Targets: []Target{trustedTarget(t, srv)}})
	rec := telemetrytest.New()
	for _, tc := range []struct {
		status  int
		outcome string
	}{{500, "http_error"}, {401, "authentication"}, {403, "authorization"}, {302, "http_error"}, {200, "success"}} {
		status.Store(int64(tc.status))
		err := c.Collect(context.Background(), rec.Emitter())
		if (err == nil) != (tc.status == 200) {
			t.Fatalf("status %d: %v", tc.status, err)
		}
		if got := point(t, rec, docOutcome.Name).Attrs[semconv.AttrReason]; got != tc.outcome {
			t.Fatalf("outcome = %s", got)
		}
		if got := point(t, rec, docStatus.Name).Value; got != float64(tc.status) {
			t.Fatalf("status = %v", got)
		}
		want := 0.0
		if tc.status == 200 {
			want = 1
		}
		if got := point(t, rec, docSuccess.Name).Value; got != want {
			t.Fatalf("success = %v", got)
		}
		if point(t, rec, docLastAttempt.Name).Value <= 0 || point(t, rec, docDuration.Name).Value < 0 {
			t.Fatal("invalid attempt timing")
		}
	}
	telemetrytest.AssertCatalogAttrs(t, rec, append(Catalog(), apistate.Catalog()...), nil)
}

func TestPerTargetMetrics(t *testing.T) {
	var statuses [2]atomic.Int64
	targets := make([]Target, len(statuses))
	for index := range statuses {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(int(statuses[index].Load()))
		}))
		t.Cleanup(srv.Close)
		targets[index] = trustedTarget(t, srv)
		targets[index].Cluster = fmt.Sprintf("target-%d", index+1)
	}
	c := New(Options{Targets: targets})
	rec := telemetrytest.New()
	for _, codes := range [][2]int{{200, 200}, {200, 503}, {503, 200}} {
		t.Run(fmt.Sprintf("%d-%d", codes[0], codes[1]), func(t *testing.T) {
			for index, code := range codes {
				statuses[index].Store(int64(code))
			}
			err := c.Collect(context.Background(), rec.Emitter())
			if (err == nil) != (codes[0] == 200 && codes[1] == 200) {
				t.Fatalf("Collect() = %v", err)
			}
			for _, doc := range Catalog() {
				points := rec.MetricPoints(doc.Name)
				wantCount := 2
				if doc.Name == docOutcome.Name {
					wantCount = 22
				}
				if len(points) != wantCount {
					t.Fatalf("%s: got %d points, want %d", doc.Name, len(points), wantCount)
				}
				seen := map[string]int{}
				for _, sample := range points {
					target := sample.Attrs[semconv.K8sAPITarget]
					index := -1
					for candidate, configured := range targets {
						if target == configured.Cluster {
							index = candidate
						}
					}
					if index < 0 {
						t.Fatalf("%s: unexpected target label %q", doc.Name, target)
					}
					seen[target]++
					want := sample.Value
					switch doc.Name {
					case docSuccess.Name:
						want = 0
						if codes[index] == 200 {
							want = 1
						}
					case docStatus.Name:
						want = float64(codes[index])
					case docOutcome.Name:
						outcome := "http_error"
						if codes[index] == 200 {
							outcome = "success"
						}
						want = 0
						if sample.Attrs[semconv.AttrReason] == outcome {
							want = 1
						}
					case docLastAttempt.Name, docValidUntil.Name:
						if sample.Value <= 0 {
							t.Fatalf("%s{%s}: invalid timestamp %v", doc.Name, target, sample.Value)
						}
					case docDuration.Name:
						if sample.Value < 0 {
							t.Fatalf("%s{%s}: invalid duration %v", doc.Name, target, sample.Value)
						}
					}
					if sample.Value != want {
						t.Fatalf("%s{%s}: got %v, want %v", doc.Name, target, sample.Value, want)
					}
				}
				for _, target := range targets {
					if seen[target.Cluster] != wantCount/len(targets) {
						t.Fatalf("%s: target counts = %v", doc.Name, seen)
					}
				}
			}
			telemetrytest.AssertCatalogAttrs(t, rec, append(Catalog(), apistate.Catalog()...), nil)
		})
	}
}

func TestBearerRotationAndTLS(t *testing.T) {
	var received atomic.Value
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	target := trustedTarget(t, srv)
	target.BearerTokenFile = filepath.Join(t.TempDir(), "token")
	c := New(Options{Targets: []Target{target}})
	rec := telemetrytest.New()
	for _, token := range []string{"first", "rotated"} {
		if err := os.WriteFile(target.BearerTokenFile, []byte(token), 0600); err != nil {
			t.Fatal(err)
		}
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatal(err)
		}
		if received.Load() != "Bearer "+token {
			t.Fatalf("authorization did not rotate")
		}
	}
	if err := os.Remove(target.BearerTokenFile); err != nil {
		t.Fatal(err)
	}
	if c.Collect(context.Background(), rec.Emitter()) == nil {
		t.Fatal("missing token succeeded")
	}
	if point(t, rec, docOutcome.Name).Attrs[semconv.AttrReason] != "configuration" {
		t.Fatal("missing token outcome")
	}
	target.CAFile, target.BearerTokenFile = "", ""
	c = New(Options{Targets: []Target{target}})
	if c.Collect(context.Background(), rec.Emitter()) == nil {
		t.Fatal("untrusted TLS succeeded")
	}
	if point(t, rec, docOutcome.Name).Attrs[semconv.AttrReason] != "tls" {
		t.Fatal("untrusted certificate outcome")
	}
}

func TestCONNECTProxy(t *testing.T) {
	var requestHost atomic.Value
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Errorf("method = %s", r.Method)
			w.WriteHeader(405)
			return
		}
		requestHost.Store(r.Host)
		upstream, err := net.Dial("tcp", srv.Listener.Addr().String())
		if err != nil {
			t.Error(err)
			w.WriteHeader(502)
			return
		}
		defer upstream.Close()
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, err := buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			t.Error(err)
			return
		}
		if err := buffer.Flush(); err != nil {
			t.Error(err)
			return
		}
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, buffer); close(done) }()
		_, _ = io.Copy(conn, upstream)
		_ = conn.Close()
		<-done
	}))
	defer proxy.Close()
	target := trustedTarget(t, srv)
	// The test certificate covers example.com. Only the proxy resolves it.
	target.URL = "https://example.com"
	rec := telemetrytest.New()
	if err := New(Options{Targets: []Target{target}, ProxyURL: proxy.URL}).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	if requestHost.Load() != "example.com:443" {
		t.Fatalf("CONNECT host = %v", requestHost.Load())
	}
	if point(t, rec, docSuccess.Name).Value != 1 {
		t.Fatal("proxy check failed")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCycleDeadlineAndUnattempted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		targets := []Target{{Cluster: "slow", URL: "https://example.com"}, {Cluster: "skipped", URL: "https://example.com"}}
		c := New(Options{Targets: targets, Concurrency: 1, Interval: time.Second, Timeout: time.Second})
		for _, client := range c.clients {
			client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
		}
		rec := telemetrytest.New()
		if c.Collect(context.Background(), rec.Emitter()) == nil {
			t.Fatal("deadline succeeded")
		}
		outcomes := rec.MetricPoints(docOutcome.Name)
		got := map[string]string{}
		for _, p := range outcomes {
			if p.Value == 1 {
				got[p.Attrs[semconv.K8sClusterName]] = p.Attrs[semconv.AttrReason]
			}
		}
		if got["slow"] != "timeout" || got["skipped"] != "unattempted" {
			t.Fatalf("outcomes: %v", got)
		}
		for _, p := range rec.MetricPoints(docSuccess.Name) {
			if p.Attrs[semconv.K8sClusterName] == "skipped" && p.Value != -1 {
				t.Fatal("unattempted check must be unknown")
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := c.Collect(ctx, rec.Emitter()); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	})
}

func TestWorkerBoundAndIsolation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var active, maximum atomic.Int64
		var targets []Target
		for i := range 8 {
			targets = append(targets, Target{Cluster: fmt.Sprint(i), URL: "https://example.com"})
		}
		c := New(Options{Targets: targets, Concurrency: 2})
		for i, client := range c.clients {
			client.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				n := active.Add(1)
				for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
				}
				defer active.Add(-1)
				time.Sleep(time.Second)
				if i == 0 {
					return nil, &net.DNSError{Err: "fixture", Name: "example.com"}
				}
				return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
			})
		}
		rec := telemetrytest.New()
		if c.Collect(context.Background(), rec.Emitter()) == nil {
			t.Fatal("failed target not reported")
		}
		if maximum.Load() != 2 {
			t.Fatalf("max concurrency: %d", maximum.Load())
		}
		points := rec.MetricPoints(docSuccess.Name)
		var successes float64
		for _, p := range points {
			successes += p.Value
		}
		if len(points) != 8 || successes != 7 {
			t.Fatalf("isolation: %+v", points)
		}
	})
}

func TestTransportFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"proxy", errProxy, "proxy"},
		{"dns", &net.DNSError{Err: "fixture"}, "dns"},
		{"connection", &net.OpError{Op: "dial", Err: errors.New("refused")}, "connection"},
		{"timeout", context.DeadlineExceeded, "timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(Options{Targets: []Target{{Cluster: "cluster", URL: "https://example.com"}}})
			c.clients[0].Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, tc.err })
			rec := telemetrytest.New()
			if c.Collect(context.Background(), rec.Emitter()) == nil {
				t.Fatal("failure succeeded")
			}
			if point(t, rec, docOutcome.Name).Attrs[semconv.AttrReason] != tc.want || point(t, rec, docStatus.Name).Value != 0 {
				t.Fatal("incorrect failure signals")
			}
		})
	}
}
