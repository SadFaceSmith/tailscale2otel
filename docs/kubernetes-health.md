---
title: Kubernetes API health
description: Check Kubernetes API readiness through a Tailscale host or sidecar
---

# Kubernetes API health

The optional `k8s_health` collector checks named cluster endpoints with HTTPS requests to `/readyz`.
HTTP 200 means the API is ready through the monitoring host's network path.
The check does not establish workload health or the health of every HA proxy replica.

```yaml
collectors:
  k8s_health:
    enabled: true
    interval: 30s
    timeout: 5s
    concurrency: 4
    proxy_url: "" # Direct access through a Tailscale-connected host.
    targets:
      - cluster: example-cluster
        url: https://api.example-tailnet.ts.net
        ca_file: "" # Empty uses system trust.
        bearer_token_file: "" # Optional for noauth proxies.
```

Each URL must be an HTTPS origin without credentials, a query, or a path beyond `/`.
The collector appends `/readyz`. It does not follow redirects or retry failed checks.
Cluster names must be unique. Setting `pii_filter.free_text_details: false` suppresses per-cluster metrics to prevent identity collisions. The collector runs explicit targets once on the primary runtime in a multi-tailnet configuration.

## Network access and authentication

The host or sidecar must already belong to the tailnet.
Tailnet grants must permit access from that identity to the API proxy's HTTPS port, usually TCP 443.
For auth-mode proxies, Kubernetes must also authorize the monitoring identity to GET the `/readyz` non-resource URL.
This collector does not configure grants, impersonation, or RBAC.

For noauth proxies, a bearer-token file can supply Kubernetes credentials.
The collector reads the file before each check, so file contents can rotate without a restart.
Keep credentials in mounted secret files. Configure each target and its file paths in YAML.
The feature does not execute kubeconfig plugins or support client certificates.

For a userspace sidecar, enable its outbound HTTP proxy and set the collector's dedicated proxy URL:

```yaml
collectors:
  k8s_health:
    proxy_url: http://localhost:1055
```

The sidecar uses `TS_OUTBOUND_HTTP_PROXY_LISTEN=localhost:1055`.
The collector sends HTTPS through HTTP CONNECT and retains the endpoint hostname for certificate verification.
It does not read `HTTP_PROXY` or `HTTPS_PROXY`. Its proxy configuration affects only these checks.
TLS verification remains enabled. A target can supply a CA file for private trust.
CA files and configuration changes require a restart.

## Results and limits

The collector exports success, duration, HTTP status, outcome, last-attempt time, and a freshness deadline for each cluster.
Success is `1` for HTTP 200, `0` for failure, and `-1` for a check skipped at the cycle deadline.
Outcomes distinguish timeouts, DNS, connection, TLS, proxy, authentication, authorization, HTTP, and local configuration failures.
A timeout does not prove an ACL denial. HTTP 401 or 403 indicates an access failure, not an API readiness verdict.

Checks use at most the configured concurrency. Each cycle ends at its interval deadline.
Skipped checks remain unknown. Remote failures do not make the exporter itself unready.
Results become stale after two intervals plus the timeout without a new attempt.

The tailnet dashboard includes a conditional **Kubernetes API Health** tab.
This feature does not include cluster alert rules.
Missing, skipped, and stale results display as unknown.

For HA endpoints, the result describes the selected service path. It does not test every backend replica.
The probes use the HTTPS API endpoint and do not require the proxy's metrics listener.
