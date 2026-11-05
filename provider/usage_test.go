package provider

import "testing"

func TestProvider_PRV13_UsageAddSumsAllFields(t *testing.T) {
	u := Usage{
		PromptTokens:     1,
		CompletionTokens: 2,
		ReasoningTokens:  3,
		TotalTokens:      4,
		CachedTokens:     5,
		CacheWriteTokens: 6,
		CostUSD:          0.25,
	}
	u.Add(Usage{
		PromptTokens:     10,
		CompletionTokens: 20,
		ReasoningTokens:  30,
		TotalTokens:      40,
		CachedTokens:     50,
		CacheWriteTokens: 60,
		CostUSD:          0.75,
	})
	if u.PromptTokens != 11 || u.CompletionTokens != 22 || u.ReasoningTokens != 33 {
		t.Errorf("tokens = %+v, want prompt 11 completion 22 reasoning 33", u)
	}
	if u.TotalTokens != 44 || u.CachedTokens != 55 || u.CacheWriteTokens != 66 || u.CostUSD != 1.0 {
		t.Errorf("totals = %+v, want total 44 cached 55 cache write 66 cost 1.0", u)
	}
}

func TestProvider_PRV25_UsageAddSumsAllowance(t *testing.T) {
	u := Usage{AllowanceUSD: 0.01, ListCostUSD: 0.04, BillingSource: "go"}
	u.Add(Usage{AllowanceUSD: 0.02, ListCostUSD: 0.08})
	if u.AllowanceUSD != 0.03 || u.ListCostUSD != 0.12 {
		t.Errorf("allowance/list = %v/%v, want 0.03/0.12", u.AllowanceUSD, u.ListCostUSD)
	}
	if u.BillingSource != "go" {
		t.Errorf("BillingSource = %q, want the reported source kept", u.BillingSource)
	}
	empty := Usage{}
	empty.Add(Usage{BillingSource: "balance"})
	if empty.BillingSource != "balance" {
		t.Errorf("BillingSource = %q, want balance", empty.BillingSource)
	}
}

func TestProvider_PRV26_AllowanceIsProviderReported(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"provider", "allowance", "provider"},
		{"allowance", "provider", "provider"},
		{"allowance", "catalog", "allowance"},
		{"catalog", "allowance", "allowance"},
		{"allowance", "allowance", "allowance"},
		{"allowance", "", "allowance"},
		{"", "allowance", "allowance"},
	}
	for _, c := range cases {
		u := Usage{CostSource: c.a}
		u.Add(Usage{CostSource: c.b})
		if u.CostSource != c.want {
			t.Errorf("Add(%q, %q) cost source = %q, want %q", c.a, c.b, u.CostSource, c.want)
		}
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
