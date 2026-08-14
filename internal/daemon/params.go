package daemon

import (
	"encoding/json"
	"strings"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

// ── Params helpers ──

func getFloat(params map[string]any, key string) float64 {
	v, _ := params[key].(float64)
	return v
}

func getInt(params map[string]any, key string) int {
	return int(getFloat(params, key))
}

func getString(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}

func parseIntParam(raw json.RawMessage, key string) int {
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return 0
	}
	return getInt(params, key)
}

func parseStringParam(raw json.RawMessage, key string) string {
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return ""
	}
	return getString(params, key)
}

func parseParams(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	return params, nil
}

// ── Params extraction helpers ──

func getMaxElementsFromParams(req *Request, defaultVal int) int {
	params, err := parseParams(req.Params)
	if err != nil {
		return defaultVal
	}
	if v, ok := params["max_elements"].(float64); ok {
		n := int(v)
		if n < 0 {
			return -1 // show all
		}
		if n == 0 {
			return defaultVal
		}
		return n
	}
	return defaultVal
}

func getFormatFromParams(req *Request) string {
	params, err := parseParams(req.Params)
	if err != nil {
		return ""
	}
	v, _ := params["format"].(string)
	return strings.ToLower(strings.TrimSpace(v))
}

func getSummaryFromParams(req *Request) bool {
	params, err := parseParams(req.Params)
	if err != nil {
		return false
	}
	v, ok := params["summary"].(bool)
	return ok && v
}

// clampCollectMax maps a client-facing max_elements value to the collect
// limit sent to the device: out-of-range values (including -1 for "all")
// mean "use the server default" (0 = protocol.DefMaxElements). Owned here so
// every handler applies the identical rule instead of repeating the clamp.
func clampCollectMax(maxShow int) int {
	if maxShow < 0 || maxShow > protocol.DefMaxElements {
		return 0
	}
	return maxShow
}
