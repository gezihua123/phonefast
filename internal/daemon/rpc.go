package daemon

import (
	"encoding/json"
	"fmt"
)

// ── JSON-RPC 2.0 types ──

// Request is a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      int64           `json:"id"`
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
	ID      int64           `json:"id"`

	// Streaming image responses (screenshot/observe): streamPrefix and
	// streamSuffix are the JSON frame parts around the base64 payload value;
	// writeStreamedResponse emits them with the payload base64-encoded
	// incrementally in between. This avoids materializing a megabyte-scale
	// base64 string plus two json.Marshal copies of it.
	// All three fields are nil for normal (non-streaming) responses.
	// Write-path only: json.Marshal on a streaming Response produces an
	// incomplete result (Result is nil); only writeResponse/writeStreamedResponse
	// can serialize it correctly.
	streamPrefix  json.RawMessage
	streamSuffix  json.RawMessage
	streamPayload []byte
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Standard JSON-RPC error codes.
const (
	ErrParse    = -32700
	ErrMethod   = -32601
	ErrInvalid  = -32602
	ErrInternal = -32603
	ErrDevice   = -32000
	ErrNoDevice = -32001
	ErrTimeout  = -32002
)

func newErrorResponse(id int64, code int, msg string) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: msg},
	}
}

func newResultResponse(id int64, result any) *Response {
	data, _ := json.Marshal(result)
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  data,
	}
}

// newStreamedImageResponse builds a Response whose Result contains a large
// base64 image field, without copying the payload or using a fragile marker
// string. Instead, the non-image fields are marshaled separately, then the
// image_data key is spliced in manually. This eliminates the risk of a field
// value colliding with a marker string.
//
// Wire format: {"jsonrpc":"2.0","result":{...nonImage,"image_data":"<b64>"},"id":N}
func newStreamedImageResponse(id int64, m map[string]any, b64Field string, payload []byte) *Response {
	// Remove the image field, marshal the rest.
	delete(m, b64Field)
	nonImage, err := json.Marshal(m)
	if err != nil {
		return newErrorResponse(id, ErrInternal, fmt.Sprintf("marshal result: %v", err))
	}

	// Build: nonImage is `{"text":"...","mime_type":"image/png"}`.
	// Strip the closing '}' and append ,"image_data":" to get the prefix.
	// The suffix is `"}` to close both the string and the result object.
	rest := nonImage[:len(nonImage)-1] // strip '}'
	prefix := make([]byte, 0, len(rest)+len(b64Field)+5)
	prefix = append(prefix, rest...)
	prefix = append(prefix, `,"`...)
	prefix = append(prefix, b64Field...)
	prefix = append(prefix, `":"`...)

	return &Response{
		JSONRPC:       "2.0",
		ID:            id,
		streamPrefix:  prefix,
		streamSuffix:  []byte{'"', '}'},
		streamPayload: payload,
	}
}
