package model

import (
	"testing"

	"github.com/LeibNici/providerapi/internal/config"
	"github.com/LeibNici/providerapi/internal/protocol"
)

func TestListAdminModelsContextLengthSource(t *testing.T) {
	cfg := &config.Config{
		Models: map[string]config.ModelAlias{
			"flash": {Provider: "p1", UpstreamModel: "up/flash"},
			"wide":  {Provider: "p1", UpstreamModel: "up/wide", ContextLength: 128000},
		},
	}
	got := ListAdminModels(cfg)
	byID := map[string]AdminModelInfo{}
	for _, m := range got {
		byID[m.ID] = m
	}
	if byID["flash"].ContextLength != protocol.DefaultContextLength {
		t.Fatalf("flash context: %+v", byID["flash"])
	}
	if byID["flash"].ContextLengthSource != "fallback" {
		t.Fatalf("flash source: %+v", byID["flash"])
	}
	if byID["wide"].ContextLength != 128000 || byID["wide"].ContextLengthSource != "configured" {
		t.Fatalf("wide: %+v", byID["wide"])
	}
}
