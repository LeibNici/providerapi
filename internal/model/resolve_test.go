package model

import (
	"testing"

	"github.com/LeibNici/providerapi/internal/config"
	"github.com/LeibNici/providerapi/internal/protocol"
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
	got, err = Resolve(cfg, "sonnet-high", protocol.ReasoningLow)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reasoning != protocol.ReasoningLow {
		t.Fatalf("explicit request must override alias high: %+v", got)
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

func TestListAliasesContextLength(t *testing.T) {
	cfg := &config.Config{
		Models: map[string]config.ModelAlias{
			"flash": {Provider: "openrouter", UpstreamModel: "deepseek/deepseek-v4.1-flash"},
			"wide":  {Provider: "openrouter", UpstreamModel: "x", ContextLength: 128000},
		},
		Providers: map[string]config.ProviderInstance{"openrouter": {Plugin: "openai-compat"}},
	}
	got := ListAliases(cfg)
	byID := map[string]protocol.ModelInfo{}
	for _, m := range got {
		byID[m.ID] = m
	}
	if byID["flash"].ContextLength != protocol.DefaultContextLength {
		t.Fatalf("default context_length: %+v", byID["flash"])
	}
	if byID["wide"].ContextLength != 128000 {
		t.Fatalf("explicit context_length: %+v", byID["wide"])
	}
}
