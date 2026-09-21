package trace

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/chenming/providerapi/internal/protocol"
	"github.com/chenming/providerapi/internal/redact"
)

func summarizeClientRaw(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"size": 0}
	}
	out := map[string]any{"size": len(b)}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return out
	}
	if model, ok := m["model"]; ok {
		var s string
		if json.Unmarshal(model, &s) == nil {
			out["model"] = s
		}
	}
	if stream, ok := m["stream"]; ok {
		var s bool
		if json.Unmarshal(stream, &s) == nil {
			out["stream"] = s
		}
	}
	return out
}

func summarizeCompletionRequest(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var req protocol.CompletionRequest
	if json.Unmarshal(b, &req) != nil {
		return map[string]any{}
	}
	out := map[string]any{
		"model":     req.Model,
		"reasoning": string(req.Reasoning.Level),
	}
	if names := toolNames(req.Tools); len(names) > 0 {
		out["tools"] = names
		out["tool_count"] = len(names)
	}
	return out
}

func toolNames(tools []protocol.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if t.Function != nil && t.Function.Name != "" {
			names = append(names, t.Function.Name)
			continue
		}
		if t.Custom != nil && t.Custom.Name != "" {
			names = append(names, t.Custom.Name)
			continue
		}
		if t.Type != "" {
			names = append(names, t.Type)
		}
	}
	return names
}

func summarizeProviderAudit(m map[string]any) map[string]any {
	out := map[string]any{"type": m["type"]}
	if method, ok := m["method"].(string); ok && method != "" {
		out["method"] = method
	}
	switch status := m["status"].(type) {
	case float64:
		out["status"] = int(status)
	case int:
		out["status"] = status
	case int64:
		out["status"] = int(status)
	}
	if rawURL, ok := m["url"].(string); ok && rawURL != "" {
		out["url"] = stripURLQuery(rawURL)
	}
	if headers, ok := m["headers"].(map[string]any); ok {
		hdrs := map[string]string{}
		for k, v := range headers {
			if s, ok := v.(string); ok {
				hdrs[k] = s
			}
		}
		out["headers"] = redact.Headers(hdrs)
	} else if headers, ok := m["headers"].(map[string]string); ok {
		out["headers"] = redact.Headers(headers)
	}
	return out
}

func stripURLQuery(raw string) string {
	if i := strings.Index(raw, "?"); i >= 0 {
		raw = raw[:i]
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return raw
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, path)
}
