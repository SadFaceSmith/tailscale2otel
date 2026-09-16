// Package k8shealth checks Kubernetes API readiness through an existing network path.
package k8shealth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/apistate"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/safefile"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// Target identifies one stable cluster endpoint and its optional credentials.
type Target struct {
	Cluster, URL, CAFile, BearerTokenFile string
}

// Options configures bounded readiness checks. ProxyURL applies only to these checks.
type Options struct {
	Targets           []Target
	Interval, Timeout time.Duration
	Concurrency       int
	ProxyURL          string
	APIState          *apistate.Tracker
}

// Collector owns the clients and the latest attempt timestamps.
type Collector struct {
	opts        Options
	clients     []*http.Client
	lastAttempt []float64
	gauges      *telemetry.GaugeSnapshotBuilder
}

var _ collector.SnapshotCollector = (*Collector)(nil)

// New prepares clients. Invalid trust material disables that target until restart.
func New(opts Options) *Collector {
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Second
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	c := &Collector{opts: opts, clients: make([]*http.Client, len(opts.Targets)), lastAttempt: make([]float64, len(opts.Targets)), gauges: telemetry.NewGaugeSnapshotBuilder()}
	for i, target := range opts.Targets {
		c.clients[i], _ = newClient(target, opts)
	}
	return c
}

func newClient(target Target, opts Options) (*http.Client, error) {
	u, err := url.Parse(target.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("invalid Kubernetes API origin")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	// A fresh connection measures the entire path and prevents transport retries
	// on a reused connection from hiding an intermittent failure.
	tr.DisableKeepAlives = true
	tr.MaxResponseHeaderBytes = 64 * 1024
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if target.CAFile != "" {
		pem, err := safefile.ReadRegular(target.CAFile, safefile.MaxPEMBytes, safefile.AllowSymlink)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("invalid CA file")
		}
		tr.TLSClientConfig.RootCAs = pool
	}
	if opts.ProxyURL != "" {
		proxy, err := url.Parse(opts.ProxyURL)
		if err != nil || proxy.Scheme != "http" || proxy.Hostname() == "" || proxy.User != nil || (proxy.Path != "" && proxy.Path != "/") || proxy.RawQuery != "" || proxy.ForceQuery || proxy.Fragment != "" {
			return nil, errors.New("invalid CONNECT proxy origin")
		}
		tr.Proxy = http.ProxyURL(proxy)
		tr.OnProxyConnectResponse = func(_ context.Context, _ *url.URL, _ *http.Request, response *http.Response) error {
			if response.StatusCode != http.StatusOK {
				return errProxy
			}
			return nil
		}
	}
	return &http.Client{Transport: tr, Timeout: opts.Timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

var errProxy = errors.New("CONNECT proxy rejected the connection")

// Name is the stable collector identifier.
func (*Collector) Name() string { return "k8s_health" }

// DefaultInterval returns the configured cadence.
func (c *Collector) DefaultInterval() time.Duration { return c.opts.Interval }

type result struct {
	at       time.Time
	duration time.Duration
	status   int
	outcome  string
}

// Collect checks targets with a worker pool and publishes one complete snapshot.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	cycle, cancel := context.WithTimeout(ctx, c.opts.Interval)
	defer cancel()
	results := make([]result, len(c.opts.Targets))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(c.opts.Concurrency, len(results)) {
		workers.Go(func() {
			for i := range jobs {
				if cycle.Err() == nil {
					results[i] = c.probe(cycle, i)
				}
			}
		})
	}
dispatch:
	for i := range results {
		select {
		case <-cycle.Done():
			break dispatch
		case jobs <- i:
		}
	}
	close(jobs)
	workers.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var firstError error
	attempts := 0
	for i, r := range results {
		attrs := telemetry.Attrs{semconv.K8sClusterName: c.opts.Targets[i].Cluster}
		if r.at.IsZero() {
			r.outcome = "unattempted"
			c.add(docSuccess, -1, attrs)
		} else {
			attempts++
			c.lastAttempt[i] = float64(r.at.Unix())
			success := 0.0
			if r.outcome == "success" {
				success = 1
			} else if firstError == nil {
				firstError = fmt.Errorf("k8s readiness probe: %s", r.outcome)
			}
			c.add(docSuccess, success, attrs)
			c.add(docDuration, r.duration.Seconds(), attrs)
			c.add(docStatus, float64(r.status), attrs)
		}
		c.add(docLastAttempt, c.lastAttempt[i], attrs)
		validUntil := 0.0
		if c.lastAttempt[i] > 0 {
			validUntil = c.lastAttempt[i] + (2*c.opts.Interval + c.opts.Timeout).Seconds()
		}
		c.add(docValidUntil, validUntil, attrs)
		for _, outcome := range []string{"success", "timeout", "dns", "connection", "tls", "proxy", "authentication", "authorization", "http_error", "configuration", "unattempted"} {
			value := 0.0
			if outcome == r.outcome {
				value = 1
			}
			c.add(docOutcome, value, telemetry.Attrs{semconv.K8sClusterName: c.opts.Targets[i].Cluster, semconv.AttrReason: outcome})
		}
	}
	c.gauges.Flush(e)
	if attempts > 0 {
		// Cluster credentials are distinct from control-plane credentials. Keep
		// their detailed failures in the per-cluster outcome, not API-scope alerts.
		apistate.Observe(e, c.opts.APIState, c.Name(), "probe_k8s_readiness", apistate.Disposition{}, firstError, time.Now())
	}
	if firstError != nil {
		return firstError
	}
	return cycle.Err()
}

func (c *Collector) add(doc metricdoc.Metric, value float64, attrs telemetry.Attrs) {
	c.gauges.Add(doc.Name, doc.Unit, doc.Description, value, attrs)
}

func (c *Collector) probe(ctx context.Context, i int) (r result) {
	r = result{at: time.Now(), outcome: "configuration"}
	defer func() { r.duration = time.Since(r.at) }()
	client := c.clients[i]
	if client == nil {
		return r
	}
	target := c.opts.Targets[i]
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(target.URL, "/")+"/readyz", nil)
	if err != nil {
		return r
	}
	if target.BearerTokenFile != "" {
		token, err := safefile.ReadRegular(target.BearerTokenFile, safefile.MaxSecretBytes, safefile.AllowSymlink)
		if err != nil || strings.TrimSpace(string(token)) == "" || strings.ContainsAny(strings.TrimSpace(string(token)), "\r\n") {
			return r
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	}
	response, err := client.Do(req)
	if err != nil {
		r.outcome = classify(err)
		return r
	}
	_ = response.Body.Close()
	r.status = response.StatusCode
	switch r.status {
	case http.StatusOK:
		r.outcome = "success"
	case http.StatusUnauthorized:
		r.outcome = "authentication"
	case http.StatusForbidden:
		r.outcome = "authorization"
	default:
		r.outcome = "http_error"
	}
	return r
}

func classify(err error) string {
	if errors.Is(err, errProxy) {
		return "proxy"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns"
	}
	var cert *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	if errors.As(err, &cert) || errors.As(err, &record) {
		return "tls"
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "proxyconnect" {
		return "proxy"
	}
	return "connection"
}
