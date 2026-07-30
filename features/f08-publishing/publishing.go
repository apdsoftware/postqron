// Package publishing owns reliable, per-destination publication execution.
//
// Every provider call receives a durable idempotency key. An adapter is
// executable only when it provides native idempotency or reconciliation for
// the crash window between a remote side effect and its local commit.
package publishing

const FeatureID = "publishing"
