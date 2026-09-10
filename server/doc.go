// Package server provides the cgo-free HTTP client for a remote Talon Server.
//
// It owns health, KV, conditional transaction v2, exact receipt lookup,
// conditional point-read v1, and bounded same-snapshot read v1. It does not
// expose embedded DB operations or revision streams. Importing this package
// does not compile or link the native Talon library.
package server
