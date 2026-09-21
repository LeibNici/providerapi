package model

import (
	"testing"

	"github.com/chenming/providerapi/internal/config"
	"github.com/chenming/providerapi/internal/protocol"
)

func TestResolvePriority(t *testing.T) {
	cfg := &config.Config{
		Models: map[string]config.ModelAlias{
			"sonnet-high": {Provider: "openrouter", UpstreamModel: "anthropic/claude-sonnet-4.5", Reasoning: "high"},
		},
		Providers: map[string]config.ProviderInstance{"openrouter": {Plugin: "openai-compat"}},
	}
	got, err := Resolve(cfg, "sonnet-high", protocol.ReasoningDefault)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpstreamModel != "anthropic/claude-sonnet-4.5" || got.Reasoning != protocol.ReasoningHigh {
		t.Fatalf("%+v", got)
	}
	got, err = Resolve(cfg, "sonnet-high", protocol.ReasoningMax)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reasoning != protocol.ReasoningMax {
		t.Fatalf("request reasoning should win: %+v", got)
	}
	if _, err := Resolve(cfg, "missing", protocol.ReasoningDefault); err == nil {
		t.Fatal("expected model not found")
	}
}
