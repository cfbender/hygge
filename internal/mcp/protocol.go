package mcp

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion is the MCP wire version hygge speaks.
const ProtocolVersion = "2024-11-05"

// JSON-RPC 2.0 method names used by MCP.
const (
	MethodInitialize  = "initialize"
	MethodInitialized = "notifications/initialized"
	MethodToolsList   = "tools/list"
	MethodToolsCall   = "tools/call"
	MethodPing        = "ping"
)

// RPCRequest is a JSON-RPC 2.0 request or notification.  When ID is
// omitted the message is a notification (server does not respond).
type RPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// RPCResponse is a JSON-RPC 2.0 response.  Exactly one of Result or
// Error is populated.
type RPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements the error interface so RPCError can flow through
// errors.Is / errors.As.
func (e *RPCError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// InitializeParams is the payload for the "initialize" request.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      ClientInfo         `json:"clientInfo"`
}

// ClientCapabilities is the bag of capabilities the client advertises.
// In v0.2 hygge declares none — the struct is reserved so adding fields
// later does not change the wire shape.
type ClientCapabilities struct {
	Roots *RootsCapability `json:"roots,omitempty"`
}

// RootsCapability describes the client's "roots" capability.  Reserved
// for v0.3; in v0.2 hygge never populates it.
type RootsCapability struct {
	ListChanged bool `json:"listChanged"`
}

// ClientInfo identifies the client to the server.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is the payload of the "initialize" response.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
}

// ServerCapabilities describes what the server supports.
type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
}

// ToolsCapability — set when the server advertises a tool catalog.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability is reserved for v0.3.  Presence is decoded but
// hygge does not yet consume resources.
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptsCapability is reserved for v0.3.  Presence is decoded but
// hygge does not yet consume prompts.
type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerInfo identifies the server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ListToolsResult is the payload of a successful "tools/list" response.
type ListToolsResult struct {
	Tools []MCPToolDef `json:"tools"`
}

// MCPToolDef describes a single tool the server offers.  InputSchema
// is an opaque JSON Schema map passed through to the provider unchanged.
type MCPToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

// CallToolParams is the payload for "tools/call".
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallToolResult is the payload of a successful "tools/call" response.
// IsError is true when the server signalled a tool-level failure; this
// is NOT an RPC error and the caller should forward the content blocks
// to the model as a normal error result.
type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock is one piece of structured tool output.  hygge v0.2
// renders "text" blocks verbatim and emits a placeholder for any other
// type (e.g. "image"); the binary fields are decoded but ignored.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}
