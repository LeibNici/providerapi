package integration

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamingToolCalls(t *testing.T) {
	public, _ := startMockAPI(t)
	raw, _ := os.ReadFile(filepath.Join(repoRoot(t), "tests/fixtures/cursor/cursor_function_tool.json"))
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	body["stream"] = true
	payload, _ := json.Marshal(body)
	resp := postJSON(t, public+"/v1/chat/completions", payload)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d %s", resp.StatusCode, b)
	}
	sc := bufio.NewScanner(resp.Body)
	gotTool := false
	gotDone := false
	finish := ""
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			gotDone = true
			break
		}
		if strings.Contains(data, "tool_calls") {
			gotTool = true
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err == nil {
			if choices, ok := chunk["choices"].([]any); ok && len(choices) > 0 {
				ch, _ := choices[0].(map[string]any)
				if fr, ok := ch["finish_reason"].(string); ok && fr != "" {
					finish = fr
				}
			}
		}
	}
	if !gotTool || !gotDone {
		t.Fatalf("stream tool incomplete tool=%v done=%v finish=%s", gotTool, gotDone, finish)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish_reason=%q", finish)
	}
}
