package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// K8sHealthConfig controls explicit Kubernetes API readiness checks.
type K8sHealthConfig struct {
	Enabled     bool              `yaml:"enabled" reload:"restart"`
	Interval    Duration          `yaml:"interval" reload:"restart"`
	Timeout     Duration          `yaml:"timeout" reload:"restart"`
	Concurrency int               `yaml:"concurrency" reload:"restart"`
	ProxyURL    string            `yaml:"proxy_url" reload:"restart"`
	Targets     []K8sHealthTarget `yaml:"targets" reload:"restart"`
}

// K8sHealthTarget names a cluster and its HTTPS API origin.
type K8sHealthTarget struct {
	Cluster         string `yaml:"cluster" reload:"restart"`
	URL             string `yaml:"url" reload:"restart"`
	CAFile          string `yaml:"ca_file" reload:"restart"`
	BearerTokenFile string `yaml:"bearer_token_file" reload:"file_content"`
}

func validateK8sHealth(c K8sHealthConfig) error {
	if !c.Enabled {
		return nil
	}
	if c.Interval.D() <= 0 || c.Timeout.D() <= 0 || c.Timeout.D() > c.Interval.D() {
		return fmt.Errorf("collectors.k8s_health requires positive interval and timeout, with timeout <= interval")
	}
	if c.Concurrency < 1 || c.Concurrency > 64 {
		return fmt.Errorf("collectors.k8s_health.concurrency must be between 1 and 64")
	}
	if len(c.Targets) == 0 {
		return fmt.Errorf("collectors.k8s_health.targets must not be empty when enabled")
	}
	if c.ProxyURL != "" && !validHealthOrigin(c.ProxyURL, "http") {
		return fmt.Errorf("collectors.k8s_health.proxy_url must be an HTTP origin without credentials, query, or fragment")
	}
	seen := make(map[string]bool, len(c.Targets))
	for i, target := range c.Targets {
		if strings.TrimSpace(target.Cluster) == "" || strings.TrimSpace(target.Cluster) != target.Cluster || seen[target.Cluster] {
			return fmt.Errorf("collectors.k8s_health.targets[%d].cluster must be non-empty, trimmed, and unique", i)
		}
		seen[target.Cluster] = true
		if !validHealthOrigin(target.URL, "https") {
			return fmt.Errorf("collectors.k8s_health.targets[%d].url must be an HTTPS origin without credentials, query, or fragment", i)
		}
	}
	return nil
}

func validHealthOrigin(raw, scheme string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != scheme || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return false
	}
	if strings.HasSuffix(u.Host, ":") {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}
