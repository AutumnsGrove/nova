package permission

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/AutumnsGrove/nova/tool"
)

// Action is what the policy decides for one tool call.
type Action string

// The three decisions. Ask defers to the user, or to the headless action when
// there is no one to ask.
const (
	Allow Action = "allow"
	Ask   Action = "ask"
	Deny  Action = "deny"
)

// actionRank orders actions by restrictiveness, so the most restrictive match
// wins regardless of rule order. An action absent from the map is invalid.
var actionRank = map[Action]int{Allow: 1, Ask: 2, Deny: 3}

// Rule matches tool calls and decides their action. All present parts must
// match: Tool by glob, every Match entry by argument glob, and at least one
// path-like argument by a Paths glob.
type Rule struct {
	// Tool is a glob over tool names. "*" matches every tool.
	Tool string
	// Match maps an argument name to a glob over that argument's string value.
	Match map[string]string
	// Paths is a list of path globs; a path-like argument must match one.
	Paths []string
	// Action is the decision when the rule matches.
	Action Action
}

// Policy is the declarative set of rules that decides tool calls.
type Policy struct {
	// Default is the action when no rule matches. Empty means the tool's
	// annotations decide.
	Default Action
	// Headless is what Ask becomes when there is no one to ask. Empty means
	// Deny. It may not be Ask.
	Headless Action
	// Rules are evaluated together; the most restrictive match wins.
	Rules []Rule
}

// Call is one tool call to decide.
type Call struct {
	Tool    string
	Args    json.RawMessage
	Info    tool.Info
	WorkDir string
}

// Decision is a policy's answer for one call.
type Decision struct {
	Action Action
	// Rule is the index of the first rule with the winning action, or -1 when
	// no rule matched.
	Rule int
	// Reason names the rule or the default that applied.
	Reason string
}

// Validate reports the first configuration error in the policy. It is meant to
// run once when a policy is loaded, not on every call.
func (p *Policy) Validate() error {
	if p.Default != "" && !validAction(p.Default) {
		return fmt.Errorf("permission: invalid default action %q", p.Default)
	}
	if p.Headless != "" && !validAction(p.Headless) {
		return fmt.Errorf("permission: invalid headless action %q", p.Headless)
	}
	if p.Headless == Ask {
		return errors.New("permission: headless action cannot be ask")
	}
	for i, r := range p.Rules {
		if r.Tool == "" {
			return fmt.Errorf("permission: rule %d: tool is required", i)
		}
		if !validAction(r.Action) {
			return fmt.Errorf("permission: rule %d: invalid action %q", i, r.Action)
		}
		if !ValidPath(r.Tool) {
			return fmt.Errorf("permission: rule %d: invalid tool glob %q", i, r.Tool)
		}
		for key, pattern := range r.Match {
			if !ValidPath(pattern) {
				return fmt.Errorf("permission: rule %d: invalid match glob %q for %q", i, pattern, key)
			}
		}
		for _, pattern := range r.Paths {
			if !ValidPath(pattern) {
				return fmt.Errorf("permission: rule %d: invalid path glob %q", i, pattern)
			}
		}
	}
	return nil
}

// Decide evaluates the policy for one call. It is pure: the same call and
// interactivity always give the same decision.
func (p *Policy) Decide(c Call, interactive bool) Decision {
	args := decodeObject(c.Args)
	best := -1
	var bestAction Action
	for i, rule := range p.Rules {
		if !ruleMatches(rule, c, args) {
			continue
		}
		if best == -1 || actionRank[rule.Action] > actionRank[bestAction] {
			best = i
			bestAction = rule.Action
		}
	}
	if best >= 0 {
		return p.resolveHeadless(Decision{Action: bestAction, Rule: best, Reason: describeRule(best, p.Rules[best])}, interactive)
	}
	if p.Default != "" {
		return p.resolveHeadless(Decision{Action: p.Default, Rule: -1, Reason: "default: " + string(p.Default)}, interactive)
	}
	action := annotationDefault(c.Info)
	return p.resolveHeadless(Decision{Action: action, Rule: -1, Reason: fmt.Sprintf("default: %s (tool annotations)", action)}, interactive)
}

// resolveHeadless turns Ask into Headless when there is no one to ask.
func (p *Policy) resolveHeadless(d Decision, interactive bool) Decision {
	if d.Action != Ask || interactive {
		return d
	}
	headless := p.Headless
	if !validAction(headless) || headless == Ask {
		headless = Deny
	}
	d.Action = headless
	d.Reason += fmt.Sprintf("; headless -> %s", headless)
	return d
}

// annotationDefault maps a tool's advisory annotations to a default action:
// read-only tools are allowed, everything else asks.
func annotationDefault(info tool.Info) Action {
	if info.ReadOnly && !info.Destructive && !info.OpenWorld {
		return Allow
	}
	return Ask
}

// validAction reports whether a is one of the three actions.
func validAction(a Action) bool { _, ok := actionRank[a]; return ok }

// ruleMatches reports whether every present part of a rule matches the call.
func ruleMatches(r Rule, c Call, args map[string]any) bool {
	if r.Tool != "" && !MatchString(r.Tool, c.Tool) {
		return false
	}
	for key, pattern := range r.Match {
		value, ok := args[key].(string)
		if !ok || !MatchString(pattern, value) {
			return false
		}
		if r.Action == Allow && HasShellControlOperator(value) {
			return false
		}
	}
	if len(r.Paths) == 0 {
		return true
	}
	candidates := pathCandidates(args, c.WorkDir)
	for _, candidate := range candidates {
		for _, pattern := range r.Paths {
			if MatchPath(pattern, candidate) {
				return true
			}
		}
	}
	return false
}

// pathCandidates resolves every path-like argument against WorkDir and returns
// the absolute forms plus, when the path stays inside WorkDir, the relative
// forms. A path that escapes WorkDir is only offered in its cleaned absolute
// form, so `../../etc/passwd` cannot masquerade as a relative path. Empty and
// NUL-containing paths are skipped.
func pathCandidates(args map[string]any, workDir string) []string {
	var raw []string
	for _, key := range []string{"path", "file", "file_path", "dir", "cwd"} {
		if s, ok := args[key].(string); ok {
			raw = append(raw, s)
		}
	}
	if list, ok := args["paths"].([]any); ok {
		for _, item := range list {
			if s, ok := item.(string); ok {
				raw = append(raw, s)
			}
		}
	}
	var out []string
	for _, p := range raw {
		if p == "" || strings.ContainsRune(p, 0) {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, p)
		}
		p = filepath.Clean(p)
		out = append(out, filepath.ToSlash(p))
		if rel, err := filepath.Rel(workDir, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	return out
}

// decodeObject parses tool arguments into a map. Invalid or non-object
// arguments yield nil, which makes Match and Paths rules not match.
func decodeObject(raw json.RawMessage) map[string]any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil
	}
	m, _ := value.(map[string]any)
	return m
}

// describeRule renders a rule for a decision's reason.
func describeRule(index int, r Rule) string {
	reason := fmt.Sprintf("rule %d: %s %s", index, r.Action, r.Tool)
	if len(r.Paths) > 0 {
		reason += " " + strings.Join(r.Paths, ",")
	}
	return reason
}
