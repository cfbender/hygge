// Package mcp implements an MCP (Model Context Protocol) client.
//
// Hygge speaks MCP over three transports:
//
//   - stdio (2024-11-05 spec) — each server runs as a subprocess; JSON-RPC
//     frames are exchanged over the process's stdin/stdout.
//   - sse   (2024-11-05 spec) — long-lived GET stream for server→client;
//     client→server via HTTP POST to the endpoint URL announced in the
//     first SSE event.  Configured with transport = "sse" in mcp.toml.
//   - http  (2025-03-26 spec, Streamable HTTP) — a single endpoint URL
//     serves both POST (client→server) and optional GET (server→client
//     notifications).  The server responds to POSTs with either a JSON
//     body or an SSE stream.  Configured with transport = "http" in
//     mcp.toml.  This is the current spec and is preferred for new
//     servers.
//
// The Client owns the JSON-RPC dispatch loop and surfaces every
// advertised tool as a hygge tool.Tool.
//
// See the package-level files for the breakdown:
//
//   - protocol.go    — JSON-RPC 2.0 types + MCP request/response shapes.
//   - framing.go     — Content-Length-framed message reader / writer.
//   - stdio.go       — subprocess Transport implementation.
//   - sse.go         — SSE Transport implementation + sseScanner / sseEvent.
//   - streamable.go  — Streamable HTTP Transport implementation.
//   - client.go      — Client lifecycle + RPC dispatch.
//   - tool.go        — MCPTool: wrap one MCP tool def as a tool.Tool.
//   - config.go      — mcp.toml loader (the .agents convention).
package mcp

import "errors"

// ErrMalformedFrame is returned by ReadFrame when the message header is
// missing a Content-Length, contains an invalid Content-Length value,
// or terminates before the body has been fully received.
var ErrMalformedFrame = errors.New("mcp: malformed frame")

// ErrClosed is returned by Client methods called after Close.  It is
// also delivered to in-flight callers whose response was still pending
// when Close happened.
var ErrClosed = errors.New("mcp: client closed")

// ErrNotInitialized is returned by Client methods (ListTools, CallTool,
// Ping) called before Initialize succeeded.
var ErrNotInitialized = errors.New("mcp: client not initialized")
