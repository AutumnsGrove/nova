package provider

import "testing"

func TestProvider_PRV13_UsageAddSumsAllFields(t *testing.T) {
	u := Usage{
		PromptTokens:     1,
		CompletionTokens: 2,
		ReasoningTokens:  3,
		TotalTokens:      4,
		CachedTokens:     5,
		CostUSD:          0.25,
	}
	u.Add(Usage{
		PromptTokens:     10,
		CompletionTokens: 20,
		ReasoningTokens:  30,
		TotalTokens:      40,
		CachedTokens:     50,
		CostUSD:          0.75,
	})
	if u.PromptTokens != 11 || u.CompletionTokens != 22 || u.ReasoningTokens != 33 {
		t.Errorf("tokens = %+v, want prompt 11 completion 22 reasoning 33", u)
	}
	if u.TotalTokens != 44 || u.CachedTokens != 55 || u.CostUSD != 1.0 {
		t.Errorf("totals = %+v, want total 44 cached 55 cost 1.0", u)
	}
}

func TestProvider_PRV14_UsageAddCostSource(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"provider", "catalog", "provider"},
		{"catalog", "provider", "provider"},
		{"catalog", "", "catalog"},
		{"", "catalog", "catalog"},
		{"", "", ""},
	}
	for _, c := range cases {
		u := Usage{CostSource: c.a}
		u.Add(Usage{CostSource: c.b})
		if u.CostSource != c.want {
			t.Errorf("Add(%q, %q) cost source = %q, want %q", c.a, c.b, u.CostSource, c.want)
		}
	}
}
