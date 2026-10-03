"""Kubernetes API readiness through the monitoring host's tailnet path."""

from builder import DASHBOARD, panel, prom_t, row, sentinel, stat_opts, thr, ts_custom, ts_opts

PREFIX = "tailscale_k8s_health_"


def fresh(metric):
    return (metric + " and on (job, instance, tailscale_tailnet, k8s_cluster_name) "
            "(" + PREFIX + "valid_until_seconds > time())")


def tab_k8shealth(scope):
    sentinel("has_k8s_health", PREFIX + "last_attempt_seconds", DASHBOARD)
    readiness = [
        (panel("Kubernetes API readiness", "stat",
               [prom_t(fresh(PREFIX + "success_ratio"), legend="{{k8s_cluster_name}}", instant=True)],
               min_=-1, max_=1, options=stat_opts(color="background"),
               mappings=[{"type": "value", "options": {
                   "-1": {"text": "Unknown", "color": "gray"},
                   "0": {"text": "Failed", "color": "red"},
                   "1": {"text": "Ready", "color": "green"}}}],
               thresholds=thr([(None, "gray"), (0, "red"), (1, "green")]),
               novalue="Unknown: no fresh readiness attempt. Check collector health and target configuration.",
               desc="HTTP 200 from /readyz means the API is ready through this tailnet path. "
                    "Skipped checks and stale results are unknown. This does not measure workload health."), 12, 5),
        (panel("Kubernetes API check latency", "timeseries",
               [prom_t(fresh(PREFIX + "duration_seconds"), legend="{{k8s_cluster_name}}")],
               unit="s", custom=ts_custom(), options=ts_opts(),
               desc="Time from check start to response headers, including connection and TLS setup."), 12, 5),
    ]
    details = [
        (panel("Kubernetes API HTTP status", "stat",
               [prom_t(fresh(PREFIX + "http_status_ratio"), legend="{{k8s_cluster_name}}", instant=True)],
               options=stat_opts(), novalue="Unknown: no fresh readiness attempt. Check collector health and target configuration.",
               desc="Latest HTTP status. Zero means no API response arrived. 401 and 403 indicate access failures."), 6, 5),
        (panel("Kubernetes API probe outcome", "stat",
               [prom_t(fresh("(" + PREFIX + "outcome_ratio == 1)"),
                       legend="{{k8s_cluster_name}}: {{reason}}", instant=True)],
               options=stat_opts(text="name"), novalue="Unknown: no fresh readiness attempt. Check collector health and target configuration.",
               desc="Current bounded outcome. A timeout does not establish whether an ACL blocked the request."), 6, 5),
        (panel("Kubernetes API last attempt age", "stat",
               [prom_t("time() - (" + PREFIX + "last_attempt_seconds > 0)",
                       legend="{{k8s_cluster_name}}", instant=True)],
               unit="s", options=stat_opts(), novalue="No attempt yet. Check collector health and target configuration.",
               desc="Seconds since the last attempt. Results expire after two configured intervals plus the timeout."), 6, 5),
        (panel("Kubernetes API result freshness", "stat",
               [prom_t("(" + PREFIX + "valid_until_seconds > 0) - time()",
                       legend="{{k8s_cluster_name}}", instant=True)],
               unit="s", options=stat_opts(),
               thresholds=thr([(None, "red"), (0, "green")]),
               novalue="No result yet. Check collector health and target configuration.",
               desc="Seconds until the latest result expires. Negative values mean the result is stale."), 6, 5),
    ]
    return [row("Cluster readiness", readiness), row("Probe details", details)]
