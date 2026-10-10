package permission

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/tool"
)

// call builds a Call with JSON arguments.
func call(name, args string, info tool.Info, workDir string) Call {
	return Call{Tool: name, Args: json.RawMessage(args), Info: info, WorkDir: workDir}
}

// readOnly is the annotation set that defaults to allow.
var readOnly = tool.Info{Name: "read", ReadOnly: true}

// TestPolicy_PRM01_ReadOnlyDefault checks a read-only tool is allowed.
func TestPolicy_PRM01_ReadOnlyDefault(t *testing.T) {
	d := (&Policy{}).Decide(call("read", `{}`, readOnly, "/w"), true)
	if d.Action != Allow || d.Rule != -1 {
		t.Fatalf("Decision = %+v, want allow", d)
	}
}

// TestPolicy_PRM02_Destructive checks a destructive tool asks by default.
func TestPolicy_PRM02_Destructive(t *testing.T) {
	d := (&Policy{}).Decide(call("rm", `{}`, tool.Info{Destructive: true}, "/w"), true)
	if d.Action != Ask {
		t.Fatalf("Decision = %+v, want ask", d)
	}
}

// TestPolicy_PRM03_OpenWorld checks an open-world tool asks by default.
func TestPolicy_PRM03_OpenWorld(t *testing.T) {
	d := (&Policy{}).Decide(call("fetch", `{}`, tool.Info{OpenWorld: true}, "/w"), true)
	if d.Action != Ask {
		t.Fatalf("Decision = %+v, want ask", d)
	}
}

// TestPolicy_PRM04_DefaultAllow checks an explicit default ignores annotations.
func TestPolicy_PRM04_DefaultAllow(t *testing.T) {
	d := (&Policy{Default: Allow}).Decide(call("rm", `{}`, tool.Info{Destructive: true}, "/w"), true)
	if d.Action != Allow {
		t.Fatalf("Decision = %+v, want allow", d)
	}
}

// TestPolicy_PRM05_DefaultDeny checks an explicit deny ignores annotations.
func TestPolicy_PRM05_DefaultDeny(t *testing.T) {
	d := (&Policy{Default: Deny}).Decide(call("read", `{}`, readOnly, "/w"), true)
	if d.Action != Deny {
		t.Fatalf("Decision = %+v, want deny", d)
	}
}

// TestPolicy_PRM06_ToolRule checks a tool-name rule decides and reports why.
func TestPolicy_PRM06_ToolRule(t *testing.T) {
	p := &Policy{Rules: []Rule{{Tool: "bash", Action: Allow}}}
	d := p.Decide(call("bash", `{}`, tool.Info{}, "/w"), true)
	if d.Action != Allow || d.Rule != 0 {
		t.Fatalf("Decision = %+v, want allow by rule 0", d)
	}
}

// TestPolicy_PRM07_ToolGlob checks a tool glob matches.
func TestPolicy_PRM07_ToolGlob(t *testing.T) {
	p := &Policy{Default: Deny, Rules: []Rule{{Tool: "mcp__*", Action: Allow}}}
	if d := p.Decide(call("mcp__x__y", `{}`, tool.Info{}, "/w"), true); d.Action != Allow {
		t.Fatalf("Decision = %+v, want allow", d)
	}
}

// TestPolicy_PRM08_MostRestrictive checks deny wins regardless of order.
func TestPolicy_PRM08_MostRestrictive(t *testing.T) {
	p := &Policy{Rules: []Rule{
		{Tool: "bash", Action: Allow},
		{Tool: "bash", Action: Deny},
	}}
	if d := p.Decide(call("bash", `{}`, tool.Info{}, "/w"), true); d.Action != Deny {
		t.Fatalf("Decision = %+v, want deny", d)
	}
}

// TestPolicy_PRM09_MostRestrictiveReversed checks deny still wins reversed.
func TestPolicy_PRM09_MostRestrictiveReversed(t *testing.T) {
	p := &Policy{Rules: []Rule{
		{Tool: "bash", Action: Deny},
		{Tool: "bash", Action: Allow},
	}}
	d := p.Decide(call("bash", `{}`, tool.Info{}, "/w"), true)
	if d.Action != Deny || d.Rule != 0 {
		t.Fatalf("Decision = %+v, want deny by rule 0", d)
	}
}

// TestPolicy_PRM10_AskBeatsAllow checks ask is more restrictive than allow.
func TestPolicy_PRM10_AskBeatsAllow(t *testing.T) {
	p := &Policy{Rules: []Rule{
		{Tool: "bash", Action: Allow},
		{Tool: "bash", Action: Ask},
	}}
	if d := p.Decide(call("bash", `{}`, tool.Info{}, "/w"), true); d.Action != Ask {
		t.Fatalf("Decision = %+v, want ask", d)
	}
}

// TestPolicy_PRM11_MatchArgument checks an argument glob decides the rule.
func TestPolicy_PRM11_MatchArgument(t *testing.T) {
	p := &Policy{Default: Deny, Rules: []Rule{{
		Tool:   "bash",
		Match:  map[string]string{"command": "git status*"},
		Action: Allow,
	}}}
	if d := p.Decide(call("bash", `{"command":"git status -s"}`, tool.Info{}, "/w"), true); d.Action != Allow {
		t.Fatalf("Decision = %+v, want allow", d)
	}
}

// TestPolicy_PRM12_MatchMissingOrNonString checks a missing or non-string
// argument never matches.
func TestPolicy_PRM12_MatchMissingOrNonString(t *testing.T) {
	p := &Policy{Default: Deny, Rules: []Rule{{
		Tool:   "bash",
		Match:  map[string]string{"command": "git status*"},
		Action: Allow,
	}}}
	for _, args := range []string{`{}`, `{"command":5}`, `{"other":"git status"}`} {
		if d := p.Decide(call("bash", args, tool.Info{}, "/w"), true); d.Action != Deny {
			t.Errorf("Decide(%s) = %+v, want deny", args, d)
		}
	}
}

// TestPolicy_PRM13_ShellSemicolon checks an allow rule does not match a
// command smuggling another past it with `;`.
func TestPolicy_PRM13_ShellSemicolon(t *testing.T) {
	p := &Policy{Default: Deny, Rules: []Rule{{
		Tool:   "bash",
		Match:  map[string]string{"command": "git status*"},
		Action: Allow,
	}}}
	if d := p.Decide(call("bash", `{"command":"git status; rm -rf /"}`, tool.Info{}, "/w"), true); d.Action != Deny {
		t.Fatalf("Decision = %+v, want deny", d)
	}
}

// TestPolicy_PRM14_ShellOperators checks every control operator blocks an allow.
func TestPolicy_PRM14_ShellOperators(t *testing.T) {
	operators := []string{";", "&&", "||", "|", "&", "`", "$(", "<(", ">", "<", "\n", "\r"}
	p := &Policy{Default: Deny, Rules: []Rule{{
		Tool:   "bash",
		Match:  map[string]string{"command": "git *"},
		Action: Allow,
	}}}
	for _, op := range operators {
		command, err := json.Marshal("git status" + op + "rm -rf /")
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		args := fmt.Sprintf(`{"command":%s}`, command)
		if d := p.Decide(call("bash", args, tool.Info{}, "/w"), true); d.Action != Deny {
			t.Errorf("operator %q: Decision = %+v, want deny", op, d)
		}
	}
}

// TestPolicy_PRM15_DenyWithOperator checks a deny rule still matches when the
// argument contains an operator.
func TestPolicy_PRM15_DenyWithOperator(t *testing.T) {
	p := &Policy{Rules: []Rule{{
		Tool:   "bash",
		Match:  map[string]string{"command": "*rm*"},
		Action: Deny,
	}}}
	d := p.Decide(call("bash", `{"command":"ls; rm -rf x"}`, tool.Info{}, "/w"), true)
	if d.Action != Deny || d.Rule != 0 {
		t.Fatalf("Decision = %+v, want deny by rule 0", d)
	}
}

// TestPolicy_PRM16_PathsRelative checks a relative path matches a relative glob.
func TestPolicy_PRM16_PathsRelative(t *testing.T) {
	p := &Policy{Default: Deny, Rules: []Rule{{
		Tool:   "write",
		Paths:  []string{"**/.env*"},
		Action: Allow,
	}}}
	if d := p.Decide(call("write", `{"path":"app/.env.local"}`, tool.Info{}, "/w"), true); d.Action != Allow {
		t.Fatalf("Decision = %+v, want allow", d)
	}
}

// TestPolicy_PRM17_PathsAbsolute checks an absolute path matches an absolute glob.
func TestPolicy_PRM17_PathsAbsolute(t *testing.T) {
	p := &Policy{Default: Deny, Rules: []Rule{{
		Tool:   "read",
		Paths:  []string{"/etc/**"},
		Action: Allow,
	}}}
	if d := p.Decide(call("read", `{"path":"/etc/passwd"}`, tool.Info{}, "/w"), true); d.Action != Allow {
		t.Fatalf("Decision = %+v, want allow", d)
	}
}

// TestPolicy_PRM18_DotDotEscape checks a .. path is matched in its cleaned
// absolute form.
func TestPolicy_PRM18_DotDotEscape(t *testing.T) {
	p := &Policy{Default: Deny, Rules: []Rule{{
		Tool:   "read",
		Paths:  []string{"/etc/**"},
		Action: Allow,
	}}}
	if d := p.Decide(call("read", `{"path":"../../etc/passwd"}`, tool.Info{}, "/w"), true); d.Action != Allow {
		t.Fatalf("Decision = %+v, want allow", d)
	}
}

// TestPolicy_PRM19_PathLikeKeys checks every path-like key is considered.
func TestPolicy_PRM19_PathLikeKeys(t *testing.T) {
	p := &Policy{Default: Deny, Rules: []Rule{{
		Tool:   "write",
		Paths:  []string{"**/.env*"},
		Action: Allow,
	}}}
	argSets := []string{
		`{"path":"app/.env"}`,
		`{"file":"app/.env"}`,
		`{"file_path":"app/.env"}`,
		`{"dir":"app/.env"}`,
		`{"cwd":"app/.env"}`,
		`{"paths":["app/.env"]}`,
	}
	for _, args := range argSets {
		if d := p.Decide(call("write", args, tool.Info{}, "/w"), true); d.Action != Allow {
			t.Errorf("Decide(%s) = %+v, want allow", args, d)
		}
	}
}

// TestPolicy_PRM20_NonPathKeys checks other keys never trigger a path rule.
func TestPolicy_PRM20_NonPathKeys(t *testing.T) {
	p := &Policy{Default: Deny, Rules: []Rule{{
		Tool:   "write",
		Paths:  []string{"/etc/**"},
		Action: Allow,
	}}}
	if d := p.Decide(call("write", `{"content":"/etc/passwd"}`, tool.Info{}, "/w"), true); d.Action != Deny {
		t.Fatalf("Decision = %+v, want deny", d)
	}
}

// TestPolicy_PRM21_AllConditions checks a rule needs every present part.
func TestPolicy_PRM21_AllConditions(t *testing.T) {
	p := &Policy{Default: Deny, Rules: []Rule{{
		Tool:   "write",
		Paths:  []string{"**/.env*"},
		Action: Allow,
	}}}
	if d := p.Decide(call("write", `{"path":"app/main.go"}`, tool.Info{}, "/w"), true); d.Action != Deny {
		t.Fatalf("Decision = %+v, want deny", d)
	}
}

// TestPolicy_PRM22_AskInteractive checks ask stays ask when interactive.
func TestPolicy_PRM22_AskInteractive(t *testing.T) {
	p := &Policy{Rules: []Rule{{Tool: "bash", Action: Ask}}}
	if d := p.Decide(call("bash", `{}`, tool.Info{}, "/w"), true); d.Action != Ask {
		t.Fatalf("Decision = %+v, want ask", d)
	}
}

// TestPolicy_PRM23_HeadlessDefault checks ask becomes deny when not interactive.
func TestPolicy_PRM23_HeadlessDefault(t *testing.T) {
	p := &Policy{Rules: []Rule{{Tool: "bash", Action: Ask}}}
	if d := p.Decide(call("bash", `{}`, tool.Info{}, "/w"), false); d.Action != Deny {
		t.Fatalf("Decision = %+v, want deny", d)
	}
}

// TestPolicy_PRM24_HeadlessAllow checks an explicit headless allow.
func TestPolicy_PRM24_HeadlessAllow(t *testing.T) {
	p := &Policy{Headless: Allow, Rules: []Rule{{Tool: "bash", Action: Ask}}}
	if d := p.Decide(call("bash", `{}`, tool.Info{}, "/w"), false); d.Action != Allow {
		t.Fatalf("Decision = %+v, want allow", d)
	}
}

// TestPolicy_PRM25_HeadlessIgnoresAllowDeny checks interactivity does not
// change allow or deny.
func TestPolicy_PRM25_HeadlessIgnoresAllowDeny(t *testing.T) {
	p := &Policy{Rules: []Rule{
		{Tool: "a", Action: Allow},
		{Tool: "b", Action: Deny},
	}}
	for _, interactive := range []bool{true, false} {
		if d := p.Decide(call("a", `{}`, tool.Info{}, "/w"), interactive); d.Action != Allow {
			t.Errorf("interactive=%v: allow became %s", interactive, d.Action)
		}
		if d := p.Decide(call("b", `{}`, tool.Info{}, "/w"), interactive); d.Action != Deny {
			t.Errorf("interactive=%v: deny became %s", interactive, d.Action)
		}
	}
}

// TestPolicy_PRM26_Reason checks the reason names the deciding rule.
func TestPolicy_PRM26_Reason(t *testing.T) {
	p := &Policy{Rules: []Rule{
		{Tool: "read", Action: Allow},
		{Tool: "write", Action: Deny},
	}}
	d := p.Decide(call("write", `{}`, tool.Info{}, "/w"), true)
	if !strings.Contains(d.Reason, "rule 1") || !strings.Contains(d.Reason, "write") {
		t.Fatalf("Reason = %q, want the rule index and tool", d.Reason)
	}
}

// TestPolicy_PRM27_ValidateRejects checks each invalid policy is rejected.
func TestPolicy_PRM27_ValidateRejects(t *testing.T) {
	cases := map[string]Policy{
		"empty tool":    {Rules: []Rule{{Tool: "", Action: Allow}}},
		"bad action":    {Rules: []Rule{{Tool: "a", Action: "maybe"}}},
		"bad tool glob": {Rules: []Rule{{Tool: "[a", Action: Allow}}},
		"bad match":     {Rules: []Rule{{Tool: "a", Match: map[string]string{"x": "[a"}, Action: Allow}}},
		"bad path":      {Rules: []Rule{{Tool: "a", Paths: []string{"[a"}, Action: Allow}}},
		"headless ask":  {Headless: Ask},
		"bad default":   {Default: "maybe"},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			if err := p.Validate(); err == nil {
				t.Fatalf("Validate(%s) = nil, want an error", name)
			}
		})
	}
}

// TestPolicy_PRM28_ValidateAccepts checks a valid policy passes.
func TestPolicy_PRM28_ValidateAccepts(t *testing.T) {
	p := &Policy{
		Default:  Ask,
		Headless: Deny,
		Rules: []Rule{
			{Tool: "read", Action: Allow},
			{Tool: "bash", Match: map[string]string{"command": "git *"}, Action: Allow},
			{Tool: "write", Paths: []string{"**/.env*"}, Action: Deny},
		},
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
}

// TestPolicy_PRM29_EmptyPath checks an empty path never matches a path rule.
func TestPolicy_PRM29_EmptyPath(t *testing.T) {
	p := &Policy{Default: Deny, Rules: []Rule{{Tool: "write", Paths: []string{"**"}, Action: Allow}}}
	if d := p.Decide(call("write", `{"path":""}`, tool.Info{}, "/w"), true); d.Action != Deny {
		t.Fatalf("Decision = %+v, want deny", d)
	}
}

// TestPolicy_PRM30_NulPath checks a NUL path is ignored without panicking.
func TestPolicy_PRM30_NulPath(t *testing.T) {
	p := &Policy{Default: Deny, Rules: []Rule{{Tool: "write", Paths: []string{"**"}, Action: Allow}}}
	args, err := json.Marshal(map[string]string{"path": "a\x00b"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if d := p.Decide(call("write", string(args), tool.Info{}, "/w"), true); d.Action != Deny {
		t.Fatalf("Decision = %+v, want deny", d)
	}
}

// FuzzDecide checks Decide never panics and never yields a headless ask.
func FuzzDecide(f *testing.F) {
	f.Add("bash", `{"command":"git status"}`, "allow")
	f.Add("write", `{"path":"a/.env"}`, "deny")
	f.Fuzz(func(t *testing.T, name, args, headless string) {
		p := &Policy{
			Headless: Action(headless),
			Rules: []Rule{
				{Tool: "*", Action: Allow},
				{Tool: name, Match: map[string]string{"command": "*"}, Paths: []string{"**"}, Action: Ask},
			},
		}
		d := p.Decide(Call{Tool: name, Args: json.RawMessage(args)}, false)
		switch d.Action {
		case Allow, Ask, Deny:
		default:
			t.Fatalf("unexpected action %q", d.Action)
		}
		if d.Action == Ask {
			t.Fatal("non-interactive Decide returned ask")
		}
	})
}

// TestProperty_PRM_MostRestrictive checks permuting rules never changes the
// decision's action.
func TestProperty_PRM_MostRestrictive(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	actions := []Action{Allow, Ask, Deny}
	for i := 0; i < 200; i++ {
		rules := make([]Rule, rng.Intn(6))
		for j := range rules {
			rules[j] = Rule{Tool: "*", Action: actions[rng.Intn(len(actions))]}
		}
		base := (&Policy{Rules: rules}).Decide(call("bash", `{}`, tool.Info{}, "/w"), true).Action
		for k := 0; k < 4; k++ {
			perm := append([]Rule(nil), rules...)
			rng.Shuffle(len(perm), func(a, b int) { perm[a], perm[b] = perm[b], perm[a] })
			if got := (&Policy{Rules: perm}).Decide(call("bash", `{}`, tool.Info{}, "/w"), true).Action; got != base {
				t.Fatalf("permuting rules changed %s to %s (rules %+v)", base, got, rules)
			}
		}
	}
}
