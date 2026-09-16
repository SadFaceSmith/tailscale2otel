package config

import (
	"testing"
	"time"
)

func TestK8sHealthValidation(t *testing.T) {
	base := func() K8sHealthConfig {
		c := Default().Collectors.K8sHealth
		c.Enabled = true
		c.Targets = []K8sHealthTarget{{Cluster: "cluster", URL: "https://cluster.example.com"}}
		return c
	}
	if Default().Collectors.K8sHealth.Enabled {
		t.Fatal("health checks must be opt-in")
	}
	if err := validateK8sHealth(base()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		tune func(*K8sHealthConfig)
	}{
		{"empty", func(c *K8sHealthConfig) { c.Targets = nil }},
		{"duplicate", func(c *K8sHealthConfig) { c.Targets = append(c.Targets, c.Targets[0]) }},
		{"empty name", func(c *K8sHealthConfig) { c.Targets[0].Cluster = " " }},
		{"http", func(c *K8sHealthConfig) { c.Targets[0].URL = "http://example.com" }},
		{"userinfo", func(c *K8sHealthConfig) { c.Targets[0].URL = "https://secret@example.com" }},
		{"path", func(c *K8sHealthConfig) { c.Targets[0].URL = "https://example.com/readyz" }},
		{"query", func(c *K8sHealthConfig) { c.Targets[0].URL = "https://example.com?" }},
		{"port", func(c *K8sHealthConfig) { c.Targets[0].URL = "https://example.com:99999" }},
		{"proxy", func(c *K8sHealthConfig) { c.ProxyURL = "socks5://localhost:1055" }},
		{"timeout", func(c *K8sHealthConfig) { c.Timeout = dur(time.Hour) }},
		{"concurrency", func(c *K8sHealthConfig) { c.Concurrency = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.tune(&c)
			if validateK8sHealth(c) == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
