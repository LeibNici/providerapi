package protocol

import "strings"

// ParseReasoningLevel maps request-level fields into Canonical levels.
// Only a small set of well-known aliases are accepted; unknown values error.
func ParseReasoningLevel(s string) (ReasoningLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "default":
		return ReasoningDefault, true
	case "off", "none":
		return ReasoningOff, true
	case "low":
		return ReasoningLow, true
	case "medium":
		return ReasoningMedium, true
	case "high":
		return ReasoningHigh, true
	case "max", "xhigh":
		return ReasoningMax, true
	default:
		return "", false
	}
}

func (l ReasoningLevel) IsZero() bool {
	return l == "" || l == ReasoningDefault
}
