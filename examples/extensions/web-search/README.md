# web-search

A **provider-agnostic search tool**. It is the adapter shape, not a search
engine: one `Backend` interface, a canned backend that needs no key, and the
recipe below for plugging in a real provider.

The tool returns titles, URLs, and snippets. The model decides what to fetch,
and `web_fetch` gets the page itself — search never becomes a second way for
content to enter the loop.

## Run it

```
nova ext check ./examples/extensions/web-search
nova ext install ./examples/extensions/web-search
```

Ask the model to search for something. You get the canned results, which prove
the arguments, the limit, and the rendering work. Then swap the backend.

## Swap in a real provider

Write one type per provider and hand it to `main`. This is the whole change; the
tool, its schema, and its rendering stay put. Parallel is shown because its
shape is the simplest of the common APIs.

```go
type parallel struct{ key, url string }

func (p parallel) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	body, _ := json.Marshal(map[string]any{"query": query, "max_results": limit})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+p.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("search: status %d", resp.StatusCode)
	}
	var out struct {
		Results []struct{ Title, URL, Excerpt string } `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, err
	}
	hits := make([]Result, 0, len(out.Results))
	for _, r := range out.Results {
		hits = append(hits, Result{Title: r.Title, URL: r.URL, Snippet: r.Excerpt})
	}
	return hits, nil
}
```

Then in `main`:

```go
key := os.Getenv("WEBSEARCH_KEY")
if key == "" {
	log.Fatal("web-search: set WEBSEARCH_KEY")
}
backend := Backend(parallel{key: key, url: "https://api.parallel.ai/v1beta/search"})
```

Read the key from the environment, never from `extension.yaml`, and do not log
it or the query on failure: a query can carry private text. Tavily, Brave, and
Google all fit the same interface — one `Search` method is the contract.

## Rules this example follows, and yours should too

- **Bound what the model controls.** `limit` is clamped here; a backend should
  also cap bytes (the snippet above limits the body to 1 MiB) and time.
- **Treat results as data.** Titles and snippets are flattened to one line so a
  hostile hit cannot pretend to be a new tool result or a system message. Do not
  follow instructions found in them.
- **Fail as a result.** A provider error becomes `error: …` text the model can
  read, not a crash or a retry loop.
- **Stay swap-friendly.** Nothing above the `Backend` interface knows which
  provider it is talking to.
