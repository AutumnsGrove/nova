package main

import (
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/compaction"
	"github.com/AutumnsGrove/nova/docs"
	"github.com/AutumnsGrove/nova/message"
)

func TestDocs_DOC01_TopicList(t *testing.T) {
	names := topicNames(docs.Topics())
	joined := strings.Join(names, ",")
	for _, want := range []string{"extensions", "protocol", "sdk", "examples"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("topic list is missing %q: %v", want, names)
		}
	}
}

func TestDocs_DOC02_PrintTopic(t *testing.T) {
	code, out, _ := runCLI(t, "docs", "extensions")
	if code != 0 || !strings.Contains(out, "Declarative tools") {
		t.Fatalf("exit %d, output missing the document:\n%s", code, out)
	}
}

func TestDocs_DOC03_UnknownTopic(t *testing.T) {
	code, out, _ := runCLI(t, "docs", "nonsense")
	if code != 0 || !strings.Contains(out, "unknown topic") || !strings.Contains(out, "extensions") {
		t.Fatalf("exit %d out %q, want the topic list", code, out)
	}
}

func TestDocs_DOC04_SearchRanking(t *testing.T) {
	topics := []docs.Topic{
		{Name: "alpha", Title: "alpha", Body: "a body that mentions beta once"},
		{Name: "beta", Title: "beta", Body: "no mention here"},
	}
	results := docs.Search(topics, []string{"beta"})
	if len(results) == 0 || results[0].Name != "beta" {
		t.Fatalf("title match did not rank first: %+v", results)
	}
}

func TestDocs_DOC05_TopFive(t *testing.T) {
	results := docs.Search(docs.Topics(), []string{"the", "a"})
	if len(results) > 5 {
		t.Fatalf("search returned %d results, want at most 5", len(results))
	}
}

func TestDocs_DOC06_NoResults(t *testing.T) {
	code, out, _ := runCLI(t, "docs", "search", "zzzzzznotfound")
	if code != 0 || !strings.Contains(out, "no matching topics") {
		t.Fatalf("exit %d out %q", code, out)
	}
}

func TestDocs_DOC07_NoColourWhenPiped(t *testing.T) {
	_, out, _ := runCLI(t, "docs", "protocol")
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("docs output contains ANSI escapes:\n%q", out)
	}
}

func TestDocs_DOC08_EmbeddedSet(t *testing.T) {
	names := topicNames(docs.Topics())
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "sdk") || !strings.Contains(joined, "examples") {
		t.Fatalf("embedded set lacks the sdk or examples: %v", names)
	}
	if !strings.Contains(joined, "example-hello") {
		t.Fatalf("embedded set lacks the examples: %v", names)
	}
}

func TestDocs_DOC09_AuthoringContextSize(t *testing.T) {
	est := compaction.Estimate([]message.Message{{Role: message.RoleUser, Content: docs.AuthoringContext()}})
	if est >= 40000 {
		t.Fatalf("authoring context estimates %d tokens, want under 40000", est)
	}
}

func FuzzDocsSearch(f *testing.F) {
	f.Add("protocol")
	f.Add("the quick brown fox")
	f.Add("")
	f.Add("!!!")
	f.Fuzz(func(t *testing.T, query string) {
		results := docs.Search(docs.Topics(), strings.Fields(query))
		if len(results) > 5 {
			t.Fatalf("search returned %d results for %q", len(results), query)
		}
	})
}
