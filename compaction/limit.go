package compaction

import (
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

// defaultReserve is the free space kept below the limit when Reserve is unset,
// unless ten percent of the limit is smaller.
const defaultReserve = 16384

// Settings configures compaction for one session. The zero value is usable; the
// config layer is expected to set Enabled.
type Settings struct {
	Enabled          bool // default true when the config layer builds it
	ContextLimit     int  // 0 means "use the model's own window"
	ModelWindow      int  // from the catalog; 0 if unknown
	Reserve          int  // tokens kept free; default 16384 or 10% of the limit
	KeepRecent       int  // tokens of recent history kept verbatim; default 20000
	SummaryMaxTokens int  // default 4000
	InputMaxTokens   int  // largest span sent to the summariser; default 100000
}

// withDefaults fills in the non-boolean defaults.
func (s Settings) withDefaults() Settings {
	if s.KeepRecent == 0 {
		s.KeepRecent = 20000
	}
	if s.SummaryMaxTokens == 0 {
		s.SummaryMaxTokens = 4000
	}
	if s.InputMaxTokens == 0 {
		s.InputMaxTokens = 100000
	}
	return s
}

// EffectiveLimit is the smaller non-zero of ContextLimit and ModelWindow. When
// both are unknown it is 0, and automatic compaction never triggers.
func EffectiveLimit(s Settings) int {
	switch {
	case s.ContextLimit > 0 && s.ModelWindow > 0:
		if s.ContextLimit < s.ModelWindow {
			return s.ContextLimit
		}
		return s.ModelWindow
	case s.ContextLimit > 0:
		return s.ContextLimit
	case s.ModelWindow > 0:
		return s.ModelWindow
	default:
		return 0
	}
}

// ShouldCompact reports whether the estimated context is over the trigger
// threshold. When the last response reported a usable prompt-token count, only
// the messages added since that response are estimated and added to it.
func ShouldCompact(s Settings, msgs []message.Message, last *provider.Usage) bool {
	if !s.Enabled {
		return false
	}
	limit := EffectiveLimit(s)
	if limit <= 0 {
		return false
	}
	reserve := s.Reserve
	if reserve <= 0 {
		reserve = defaultReserve
		if tenth := limit / 10; tenth < reserve {
			reserve = tenth
		}
	}
	if half := limit / 2; reserve > half {
		reserve = half
	}
	threshold := limit - reserve

	est := Estimate(msgs)
	if last != nil && last.PromptTokens > 0 && last.PromptTokens <= limit {
		// last.PromptTokens covers the request that produced the most recent
		// assistant message; everything after that message is new.
		tail := 0
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == message.RoleAssistant {
				tail = i + 1
				break
			}
		}
		est = last.PromptTokens + Estimate(msgs[tail:])
	}
	return est > threshold
}
