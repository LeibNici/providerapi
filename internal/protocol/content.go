package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Content accepts a string, a multipart array, or any other JSON value.
// It is intentionally not frozen as a single string.
type Content struct {
	Text  *string
	Parts []ContentPart
	Raw   json.RawMessage
}

type ContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL json.RawMessage `json:"image_url,omitempty"`
	Extra    json.RawMessage `json:"-"`
}

func (c *Content) UnmarshalJSON(b []byte) error {
	*c = Content{}
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return nil
	}
	c.Raw = append(json.RawMessage(nil), b...)
	switch b[0] {
	case '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		c.Text = &s
		return nil
	case '[':
		var parts []ContentPart
		if err := json.Unmarshal(b, &parts); err != nil {
			return err
		}
		c.Parts = parts
		return nil
	default:
		return nil
	}
}

func (c Content) MarshalJSON() ([]byte, error) {
	if c.Text != nil {
		return json.Marshal(*c.Text)
	}
	if c.Parts != nil {
		return json.Marshal(c.Parts)
	}
	if len(c.Raw) > 0 {
		return c.Raw, nil
	}
	return []byte(`""`), nil
}

func (c Content) String() string {
	if c.Text != nil {
		return *c.Text
	}
	if len(c.Parts) > 0 {
		var buf bytes.Buffer
		for _, p := range c.Parts {
			buf.WriteString(p.Text)
		}
		return buf.String()
	}
	if len(c.Raw) > 0 {
		return string(c.Raw)
	}
	return ""
}

func TextContent(s string) Content {
	return Content{Text: &s, Raw: mustJSON(s)}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("json marshal: %v", err))
	}
	return b
}
