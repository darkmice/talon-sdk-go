// Package server provides the cgo-free HTTP client for a remote Talon Server.
//
// It owns health, KV, conditional transaction v2, exact receipt lookup,
// conditional point-read v1, bounded same-snapshot reads v1/v2, leased
// conditional prefix-scan v1, and the versioned SQL/query surface with exact
// DECIMAL. It does not expose embedded DB operations or revision streams.
// Importing this package does not compile or link the native Talon library.
//
// # Exact DECIMAL
//
// Decimal is the exact projection of a Talon fixed-point value: a big.Int
// coefficient and a retained scale. It is written as a canonical decimal
// string ({"Decimal":"123.4500"}) and is never routed through float64. The
// precision and scale bound is MaxDecimalPrecision (38), matching Core's
// DECIMAL_MAX_PRECISION and the embedded path; out-of-range input is rejected
// rather than rounded. The JSON contract is fixed by talon-core issue #12.
//
// # Capability gate
//
// The SQL surface is gated. ServerSQLCapability reports "gated" because the
// cgo-free client has no signed artifact to read; Query fails closed with
// CodeCapabilityUnavailable until a caller that has verified a released
// talon-bin artifact attests the surface through an exact
// ServerClientConfig.ServerSQLAttestation identity. The SDK never infers
// availability from the endpoint, a tag, or a symbol name, and it never marks
// the surface available on the remote Server's behalf. Core accepts reads and
// writes on the same SQL endpoint, so a response failure after sending is
// conservatively reported as CodeResultIndeterminate; protocol violations are
// retained as wrapped diagnostic causes.
package server
