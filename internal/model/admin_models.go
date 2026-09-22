package model

import (
	"github.com/LeibNici/providerapi/internal/config"
	"github.com/LeibNici/providerapi/internal/protocol"
)

// AdminModelInfo is the admin-console view of a configured model alias.
// It is separate from protocol.ModelInfo used by /v1/models.
type AdminModelInfo struct {
	ID                  string `json:"id"`
	Provider            string `json:"provider"`
	UpstreamModel       string `json:"upstream_model"`
	Reasoning           string `json:"reasoning"`
	ContextLength       int    `json:"context_length"`
	ContextLengthSource string `json:"context_length_source"`
}

func ListAdminModels(cfg *config.Config) []AdminModelInfo {
	out := make([]AdminModelInfo, 0, len(cfg.Models))
	for id, alias := range cfg.Models {
		source := "fallback"
		n := protocol.DefaultContextLength
		if alias.ContextLength > 0 {
			source = "configured"
			n = alias.ContextLength
		}
		reasoning := alias.Reasoning
		if reasoning == "" {
			reasoning = string(protocol.ReasoningDefault)
		}
		out = append(out, AdminModelInfo{
			ID:                  id,
			Provider:            alias.Provider,
			UpstreamModel:       alias.UpstreamModel,
			Reasoning:           reasoning,
			ContextLength:       n,
			ContextLengthSource: source,
		})
	}
	return out
}
