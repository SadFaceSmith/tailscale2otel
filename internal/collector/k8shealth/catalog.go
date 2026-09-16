package k8shealth

import (
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
)

var (
	docSuccess     = descriptor("success", semconv.UnitDimensionless, "1 when the Kubernetes API readiness check returns HTTP 200, otherwise 0. Minus one means the cycle could not attempt the check.")
	docDuration    = descriptor("duration", semconv.UnitSeconds, "Duration of the latest Kubernetes API readiness attempt, including connection and TLS setup.")
	docStatus      = descriptor("http.status", semconv.UnitDimensionless, "HTTP status of the latest Kubernetes API readiness attempt. Zero means no API response arrived.")
	docLastAttempt = timestampDescriptor("last_attempt", semconv.UnitSeconds, "Unix timestamp of the latest Kubernetes API readiness attempt. Zero means no attempt occurred.")
	docValidUntil  = timestampDescriptor("valid_until", semconv.UnitSeconds, "Unix timestamp after which the latest readiness result is stale: attempt time plus two intervals and the timeout.")
	docOutcome     = metricdoc.Metric{
		Name: "tailscale.k8s.health.outcome", Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "Latest readiness outcome: success, timeout, dns, connection, tls, proxy, authentication, authorization, http_error, configuration, or unattempted.",
		Attributes:  []string{semconv.K8sClusterName, semconv.AttrReason}, Group: "Kubernetes API health",
	}
)

func descriptor(suffix, unit, description string) metricdoc.Metric {
	return metricdoc.Metric{Name: "tailscale.k8s.health." + suffix, Unit: unit, Instrument: metricdoc.Gauge,
		Description: description, Attributes: []string{semconv.K8sClusterName}, Group: "Kubernetes API health"}
}

// Catalog declares the readiness metrics.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docSuccess, docDuration, docStatus, docLastAttempt, docOutcome, docValidUntil}
}

func timestampDescriptor(suffix, unit, description string) metricdoc.Metric {
	m := descriptor(suffix, unit, description)
	m.TimeSource = metricdoc.TimestampProcessLocal
	return m
}
