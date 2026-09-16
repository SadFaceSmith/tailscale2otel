---
id: TSO-0150
title: Probe Kubernetes API readiness through tailnet proxies
status: Done
assignee:
  - '@SadFaceSmith'
created_date: '2026-09-16 01:18'
updated_date: '2026-09-16 01:50'
labels: []
dependencies: []
type: feature
ordinal: 151000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Node reachability does not establish Kubernetes API readiness. Operators need cluster checks through the same tailnet path used for API access.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Explicit HTTPS cluster targets emit readiness, latency, status, outcome, and attempt time through the existing telemetry pipeline.
- [x] #2 Direct and CONNECT proxy paths verify TLS and support rotating bearer-token files with bounded scheduling.
- [x] #3 Focused tests and repository gates pass, with any environment limitations recorded.
- [x] #4 Configuration, Helm examples, and generated dashboard JSON document and expose the feature.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement the approved Kubernetes readiness plan on the personal fork: add an opt-in collector, wire configuration and scheduling, add telemetry and generated Grafana surfaces, test failure modes, and run repository checks.

User narrowed scope: defer alert rules. Complete the collector and dashboard JSON, then provide a personal GHCR build-and-push command.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
The complete just check gate passed with pinned Go tools and temporary Helm 4.3.0. The installed Helm 3.17.3 produced incompatible fixture error text; its installation was unchanged. All six metrics have generated dashboard panels. Tests cover real local HTTPS and CONNECT requests, token rotation, TLS rejection, outcome recovery, bounded concurrency, skipped attempts, multi-runtime registration, and exporter readiness. An enabled Helm target rendered and passed configcheck. No live cluster was contacted. Alert work was deferred at the user request.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added optional Kubernetes API readiness checks through existing tailnet connectivity. Configuration, documentation, Helm defaults, catalog, and dashboard JSON are complete. The full gate passed, and just gen reproduced the staged artifacts without changes. Delivery targets the personal fork.
<!-- SECTION:FINAL_SUMMARY:END -->
