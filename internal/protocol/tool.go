package protocol

import (
	"encoding/json"
)

// UnmarshalJSON preserves Raw fidelity across JSON-RPC:
// - If the input has a canonical `raw` field, Raw is restored from it.
// - Otherwise Raw is the entire input JSON (first Cursor JSON pass).
func (t *Tool) UnmarshalJSON(data []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if raw, ok := m["raw"]; ok {
		t.Raw = raw
	} else {
		t.Raw = json.RawMessage(data)
	}
	if v, ok := m["type"]; ok {
		_ = json.Unmarshal(v, &t.Type)
	}
	if v, ok := m["function"]; ok {
		t.Function = &FunctionTool{}
		if err := json.Unmarshal(v, t.Function); err != nil {
			return err
		}
	}
	if v, ok := m["custom"]; ok {
		t.Custom = &CustomTool{}
		if err := json.Unmarshal(v, t.Custom); err != nil {
			return err
		}
	}
	return nil
}

// MarshalJSON emits canonical fields plus raw when present.
func (t Tool) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	if t.Type != "" {
		out["type"] = t.Type
	}
	if t.Function != nil {
		out["function"] = t.Function
	}
	if t.Custom != nil {
		out["custom"] = t.Custom
	}
	if len(t.Raw) > 0 {
		out["raw"] = t.Raw
	}
	return json.Marshal(out)
}

// UpstreamObject builds the upstream tool object by deep-overlaying canonical
// fields onto Raw (nested merge for function/custom).
func (t Tool) UpstreamObject() map[string]any {
	var out map[string]any
	if len(t.Raw) > 0 {
		_ = json.Unmarshal(t.Raw, &out)
	}
	if out == nil {
		out = map[string]any{}
	}
	if t.Type != "" {
		out["type"] = t.Type
	}
	if t.Function != nil {
		fn := functionToolMap(t.Function)
		if existing, ok := out["function"].(map[string]any); ok {
			deepOverlay(existing, fn)
			out["function"] = existing
		} else {
			out["function"] = fn
		}
	}
	if t.Custom != nil {
		cu := customToolMap(t.Custom)
		if existing, ok := out["custom"].(map[string]any); ok {
			deepOverlay(existing, cu)
			out["custom"] = existing
		} else {
			out["custom"] = cu
		}
	}
	return out
}

func functionToolMap(f *FunctionTool) map[string]any {
	m := map[string]any{"name": f.Name}
	if f.Description != "" {
		m["description"] = f.Description
	}
	if len(f.Parameters) > 0 {
		var params any
		if json.Unmarshal(f.Parameters, &params) == nil {
			m["parameters"] = params
		}
	}
	return m
}

func customToolMap(c *CustomTool) map[string]any {
	m := map[string]any{}
	if c.Name != "" {
		m["name"] = c.Name
	}
	if c.Description != "" {
		m["description"] = c.Description
	}
	if len(c.Input) > 0 {
		var input any
		if json.Unmarshal(c.Input, &input) == nil {
			m["input"] = input
		}
	}
	return m
}

func deepOverlay(dst, src map[string]any) {
	for k, v := range src {
		if existing, ok := dst[k].(map[string]any); ok {
			if nested, ok := v.(map[string]any); ok {
				deepOverlay(existing, nested)
				continue
			}
		}
		dst[k] = v
	}
}
