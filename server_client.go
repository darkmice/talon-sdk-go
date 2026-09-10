package talon

import serverapi "github.com/darkmice/talon-sdk-go/server"

const ServerHTTPProtocolVersion = serverapi.ServerHTTPProtocolVersion

type ServerTLSConfig = serverapi.ServerTLSConfig
type ServerClientConfig = serverapi.ServerClientConfig
type ServerHealth = serverapi.ServerHealth

// ServerClient is retained as a source-compatible alias. Server-only
// applications should import github.com/darkmice/talon-sdk-go/server directly
// so the embedded DB and cgo files are outside their package graph.
//
// Revision-stream operations are intentionally not part of this HTTP client.
type ServerClient = serverapi.ServerClient

func NewServerClient(config ServerClientConfig) (*ServerClient, error) {
	return serverapi.NewServerClient(config)
}
