package model

import (
	"github.com/LeibNici/providerapi/internal/config"
	"github.com/LeibNici/providerapi/internal/protocol"
)

type Resolved struct {
	ClientModel   string
	Provider      string
	UpstreamModel string
	Reasoning     protocol.ReasoningLevel
}

func Resolve(cfg *config.Config, clientModel string, requestReasoning protocol.ReasoningLevel) (*Resolved, error) {
	alias, ok := cfg.Models[clientModel]
	if !ok {
		return nil, protocol.ModelNotFound(clientModel)
	}
	level := protocol.ReasoningDefault
	if alias.Reasoning != "" {
		parsed, ok := protocol.ParseReasoningLevel(alias.Reasoning)
		if !ok {
			return nil, protocol.InvalidRequest("invalid model alias reasoning: " + alias.Reasoning)
		}
		level = parsed
	}
	if requestReasoning != "" && requestReasoning != protocol.ReasoningDefault {
		level = requestReasoning
	}
	return &Resolved{
		ClientModel:   clientModel,
		Provider:      alias.Provider,
		UpstreamModel: alias.UpstreamModel,
		Reasoning:     level,
	}, nil
}

func ListAliases(cfg *config.Config) []protocol.ModelInfo {
	out := make([]protocol.ModelInfo, 0, len(cfg.Models))
	for id, alias := range cfg.Models {
		n := alias.ContextLength
		if n <= 0 {
			n = protocol.DefaultContextLength
		}
		out = append(out, protocol.ModelInfo{
			ID:            id,
			Object:        "model",
			OwnedBy:       "providerapi",
			ContextLength: n,
		})
	}
	return out
}
