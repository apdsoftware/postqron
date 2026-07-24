// Package publishing owns reliable, per-destination publication execution.
//
// Every provider call receives a durable idempotency key. Provider adapters
// must return the same remote publication for every call with the same key;
// this closes the crash window between a successful remote call and the local
// transaction that records its result.
package publishing

const FeatureID = "publishing"
