// Package connectsse provides an HTTP transport layer for Connect RPC that uses
// Server-Sent Events (SSE) for streaming responses.
//
// This package implements both client and server components that translate between
// the Connect RPC protocol and an HTTP+JSON/SSE transport layer. The translation
// happens at the net/http layer:
//
//   - Client.Do implements connect.HTTPClient and converts Connect RPC requests
//     to nested HTTP+JSON requests, then parses JSON or SSE responses back to the
//     Connect RPC format.
//
//   - Server.ServeHTTP implements http.Handler and does the reverse transformation,
//     decoding nested requests and encoding responses as JSON or SSE.
//
// For unary RPCs, requests and responses use application/json. For streaming RPCs,
// responses use Server-Sent Events (text/event-stream) on the wire, but are converted
// to the Connect RPC streaming format (application/connect+json with enveloped messages).
package connectsse
