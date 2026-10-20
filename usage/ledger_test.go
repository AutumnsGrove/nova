package usage

import (
	"math"
	"sync"
	"testing"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/session"
)

// catalog maps two priced models and rejects everything else.
func catalog(id string) (provider.Model, bool) {
	switch id {
	case "a":
		return provider.Model{ID: "a", InPerM: 2, OutPerM: 4}, true
	case "b":
		return provider.Model{ID: "b", InPerM: 10, OutPerM: 20}, true
	}
	return provider.Model{}, false
}

func Test_LED01_ProviderCost(t *testing.T) {
	l := NewLedger(catalog)
	got := l.Add("a", provider.Usage{PromptTokens: 100, CompletionTokens: 50, CostUSD: 0.01})
	if got.CostUSD != 0.01 || got.CostSource != "provider" {
		t.Fatalf("usage = %+v, want provider cost", got)
	}
	if l.Total().CostUSD != 0.01 {
		t.Fatalf("total = %+v", l.Total())
	}
}

func Test_LED02_CatalogFallback(t *testing.T) {
	l := NewLedger(catalog)
	got := l.Add("a", provider.Usage{PromptTokens: 1000, CompletionTokens: 500, CachedTokens: 200})
	want := 0.00364
	if math.Abs(got.CostUSD-want) > 1e-9 || got.CostSource != "catalog" {
		t.Fatalf("usage = %+v, want catalog %.6f", got, want)
	}
}

func Test_LED03_CachedDiscount(t *testing.T) {
	m := provider.Model{ID: "x", InPerM: 1, OutPerM: 0}
	// 1000 prompt, all of it cached: every token is billed at the 10% rate.
	usd, source := CostFor(m, provider.Usage{PromptTokens: 1000, CachedTokens: 1000})
	want := (0.0 + 1000*1*0.1) / 1e6
	if math.Abs(usd-want) > 1e-12 || source != "catalog" {
		t.Fatalf("usd = %v source = %q, want %v catalog", usd, source, want)
	}
}

func Test_LED04_CachedAbovePrompt(t *testing.T) {
	m := provider.Model{ID: "x", InPerM: 1, OutPerM: 0}
	usd, _ := CostFor(m, provider.Usage{PromptTokens: 100, CachedTokens: 999})
	if usd < 0 {
		t.Fatalf("usd = %v, want non-negative", usd)
	}
	// Capped at prompt tokens: 100 * 1 / 1e6 * 0.1.
	if math.Abs(usd-100*1*0.1/1e6) > 1e-12 {
		t.Fatalf("usd = %v", usd)
	}
}

func Test_LED05_UnknownModel(t *testing.T) {
	usd, source := CostFor(provider.Model{}, provider.Usage{PromptTokens: 100})
	if usd != 0 || source != "" {
		t.Fatalf("usd = %v source = %q, want 0 / empty", usd, source)
	}
	l := NewLedger(catalog)
	got := l.Add("ghost", provider.Usage{PromptTokens: 100})
	if got.CostUSD != 0 || got.CostSource != "" {
		t.Fatalf("usage = %+v", got)
	}
}

func Test_LED06_ByModelBreakdown(t *testing.T) {
	l := NewLedger(catalog)
	l.Add("a", provider.Usage{PromptTokens: 10})
	l.Add("b", provider.Usage{PromptTokens: 20})
	by := l.ByModel()
	if by["a"].PromptTokens != 10 || by["b"].PromptTokens != 20 || len(by) != 2 {
		t.Fatalf("byModel = %+v", by)
	}
}

func Test_LED07_TotalAcrossModels(t *testing.T) {
	l := NewLedger(catalog)
	l.Add("a", provider.Usage{PromptTokens: 10, CompletionTokens: 1})
	l.Add("b", provider.Usage{PromptTokens: 20, CompletionTokens: 2})
	total := l.Total()
	if total.PromptTokens != 30 || total.CompletionTokens != 3 {
		t.Fatalf("total = %+v", total)
	}
}

func Test_LED08_RebuildFromEntries(t *testing.T) {
	entries := []session.Entry{
		{Type: session.TypeMessage, Model: "a", Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 50}},
		{Type: session.TypeCompaction, Model: "b", Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 5}},
	}
	l := NewLedger(catalog)
	l.Rebuild(entries)

	want := NewLedger(catalog)
	want.Add("a", provider.Usage{PromptTokens: 100, CompletionTokens: 50})
	want.Add("b", provider.Usage{PromptTokens: 10, CompletionTokens: 5})
	if l.Total() != want.Total() {
		t.Fatalf("total = %+v, want %+v", l.Total(), want.Total())
	}
}

func Test_LED09_RebuildIdempotent(t *testing.T) {
	entries := []session.Entry{
		{Type: session.TypeMessage, Model: "a", Usage: &provider.Usage{PromptTokens: 100}},
	}
	l := NewLedger(catalog)
	l.Rebuild(entries)
	first := l.Total()
	l.Rebuild(entries)
	if l.Total() != first {
		t.Fatalf("second Rebuild changed the total: %+v != %+v", l.Total(), first)
	}
}

func Test_LED10_RebuildIgnoresMissingUsage(t *testing.T) {
	with := []session.Entry{
		{Type: session.TypeMessage, Model: "a", Usage: &provider.Usage{PromptTokens: 7}},
		{Type: session.TypeCustom, Model: "a"}, // no usage
	}
	without := []session.Entry{
		{Type: session.TypeMessage, Model: "a", Usage: &provider.Usage{PromptTokens: 7}},
	}
	a := NewLedger(catalog)
	a.Rebuild(with)
	b := NewLedger(catalog)
	b.Rebuild(without)
	if a.Total() != b.Total() {
		t.Fatalf("totals differ: %+v != %+v", a.Total(), b.Total())
	}
}

func Test_LED11_ConcurrentAdd(t *testing.T) {
	l := NewLedger(catalog)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Add("a", provider.Usage{PromptTokens: 1, CompletionTokens: 1})
		}()
	}
	wg.Wait()
	if got := l.Total(); got.PromptTokens != 20 || got.CompletionTokens != 20 {
		t.Fatalf("total = %+v, want 20/20", got)
	}
}

func Test_LED12_NegativeOrNaN(t *testing.T) {
	m := provider.Model{ID: "x", InPerM: 2, OutPerM: 4}
	cases := []provider.Usage{
		{CostUSD: math.NaN()},
		{CostUSD: -1},
		{PromptTokens: -100, CompletionTokens: -100},
		{},
	}
	for _, u := range cases {
		usd, _ := CostFor(m, u)
		if usd < 0 || math.IsNaN(usd) {
			t.Fatalf("usage %+v gave usd %v", u, usd)
		}
	}
}

func Test_LED13_NonFiniteProviderCost(t *testing.T) {
	m := provider.Model{ID: "x", InPerM: 2, OutPerM: 4}
	tokens := provider.Usage{PromptTokens: 1000, CompletionTokens: 500}
	// Garbage costs nothing (D86); a negative or -Inf figure is just not reported.
	for _, bad := range []float64{math.NaN(), math.Inf(1)} {
		tokens.CostUSD = bad
		if usd, source := CostFor(m, tokens); usd != 0 || source != "" {
			t.Errorf("provider cost %v gave (%v, %q), want (0, empty)", bad, usd, source)
		}
	}
	for _, bad := range []float64{math.Inf(-1), -1} {
		tokens.CostUSD = bad
		if usd, source := CostFor(m, tokens); source != "catalog" || math.Abs(usd-0.004) > 1e-12 {
			t.Errorf("provider cost %v gave (%v, %q), want the catalog estimate 0.004", bad, usd, source)
		}
	}
}

func Test_LED14_RebuildIsAtomic(t *testing.T) {
	entries := []session.Entry{
		{Type: session.TypeMessage, Model: "a", Usage: &provider.Usage{PromptTokens: 1}},
		{Type: session.TypeMessage, Model: "a", Usage: &provider.Usage{PromptTokens: 2}},
		{Type: session.TypeMessage, Model: "b", Usage: &provider.Usage{PromptTokens: 4}},
	}
	l := NewLedger(catalog)
	l.Rebuild(entries)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				l.Rebuild(entries)
			}
		}
	}()
	// A reader must never see the half-built ledger a reset-then-add Rebuild
	// would expose between its first and last entry.
	for range 2000 {
		if got := l.Total().PromptTokens; got != 7 {
			close(stop)
			<-done
			t.Fatalf("Total during Rebuild = %d tokens, want 7", got)
		}
		if got := l.ByModel()["a"].PromptTokens; got != 3 {
			close(stop)
			<-done
			t.Fatalf("model a during Rebuild = %d tokens, want 3", got)
		}
	}
	close(stop)
	<-done
}
