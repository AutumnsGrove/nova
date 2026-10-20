package opencodego

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/provider"
)

// Discovery defaults.
const (
	// ModelsDevURL is the metadata source for live model ids.
	ModelsDevURL = "https://models.dev/api.json"
	// CacheMaxAge is how long a cached overlay is fresh.
	CacheMaxAge = 12 * time.Hour
	// discoveryTimeout bounds one discovery run.
	discoveryTimeout = 6 * time.Second
	// defaultContext and defaultMaxTokens fill ids models.dev does not know.
	defaultContext   = 131_072
	defaultMaxTokens = 16_384
	// maxModelsBody and maxModelsDevBody cap what a discovery response may make
	// Nova read; the models.dev catalog covers every provider, so it is larger.
	maxModelsBody    = 1 << 20
	maxModelsDevBody = 16 << 20
)

// Discovered is one live model before it is registered.
type Discovered struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Slug            string  `json:"slug"`
	ContextWindow   int     `json:"context_window"`
	MaxOutputTokens int     `json:"max_output_tokens"`
	InPerM          float64 `json:"in_per_m"`
	OutPerM         float64 `json:"out_per_m"`
	Dialect         Dialect `json:"dialect"`
}

// model converts an overlay entry to a catalog model.
func (d Discovered) model() provider.Model {
	return provider.Model{
		ID:              d.ID,
		Name:            d.Name,
		Provider:        ProviderName,
		Slug:            d.Slug,
		ContextWindow:   d.ContextWindow,
		MaxOutputTokens: d.MaxOutputTokens,
		InPerM:          d.InPerM,
		OutPerM:         d.OutPerM,
	}
}

// cacheFile is the on-disk overlay.
type cacheFile struct {
	CheckedAt int64        `json:"checked_at"`
	Models    []Discovered `json:"models"`
}

// DiscoveryConfig configures a discovery run.
type DiscoveryConfig struct {
	BaseURL      string
	APIKey       string
	HTTPClient   *http.Client
	CachePath    string
	ModelsDevURL string
	MaxAge       time.Duration
}

// setOverlay replaces the live overlay and re-registers the catalog.
func setOverlay(models []Discovered) {
	mu.Lock()
	discovered = models
	mu.Unlock()
	RegisterModels()
}

// Refresh fetches the live Go model ids, enriches them with models.dev
// metadata, saves the cache, and re-registers the catalog. A failure leaves the
// current overlay in place.
func Refresh(ctx context.Context, cfg DiscoveryConfig) error {
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	cctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()

	liveIDs, err := fetchLiveIDs(cctx, client, base, cfg.APIKey)
	if err != nil {
		return err
	}
	models := buildDiscovered(liveIDs, loadModelsDev(cctx, client, cfg.ModelsDevURL))
	if cfg.CachePath != "" {
		// The cache only saves the next start a request, so failing to write it
		// must not fail a refresh that already succeeded.
		_ = saveCache(cfg.CachePath, models)
	}
	setOverlay(models)
	return nil
}

// LoadCache loads the overlay from disk and re-registers the catalog.
func LoadCache(path string) error {
	cache, err := readCache(path)
	if err != nil {
		return err
	}
	setOverlay(cache.Models)
	return nil
}

// CacheFresh reports whether the cache exists and is younger than maxAge.
func CacheFresh(path string, maxAge time.Duration) bool {
	cache, err := readCache(path)
	if err != nil {
		return false
	}
	if maxAge <= 0 {
		maxAge = CacheMaxAge
	}
	return time.Since(time.Unix(cache.CheckedAt, 0)) < maxAge
}

// fetchLiveIDs reads the model ids from the Go models endpoint. A reply with no
// ids is an error, not an empty list: accepting it would wipe the overlay and
// stamp the cache fresh on a transient or unexpected response.
func fetchLiveIDs(ctx context.Context, client *http.Client, base, key string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("opencodego: build models request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencodego: models request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("opencodego: models request returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelsBody))
	if err != nil {
		return nil, fmt.Errorf("opencodego: read models response: %w", err)
	}
	ids := parseModelIDs(body)
	if len(ids) == 0 {
		return nil, errors.New("opencodego: models response lists no models")
	}
	return ids, nil
}

// parseModelIDs reads the ids from either {"data":[...]} or a bare array whose
// rows are id strings or objects with an id.
func parseModelIDs(body []byte) []string {
	var wrapper struct {
		Data []json.RawMessage `json:"data"`
	}
	var rows []json.RawMessage
	if json.Unmarshal(body, &wrapper) == nil && len(wrapper.Data) > 0 {
		rows = wrapper.Data
	} else {
		_ = json.Unmarshal(body, &rows) // not a list either: no rows, reported by the caller
	}
	var ids []string
	for _, row := range rows {
		var id string
		if json.Unmarshal(row, &id) != nil {
			var obj struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(row, &obj)
			id = obj.ID
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// devModel is the models.dev metadata for one model.
type devModel struct {
	Name  string `json:"name"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Cost struct {
		Input  float64 `json:"input"`
		Output float64 `json:"output"`
	} `json:"cost"`
}

// loadModelsDev reads the opencode-go metadata. It is best effort: a model
// without metadata still gets default limits, so any failure is an empty map.
func loadModelsDev(ctx context.Context, client *http.Client, url string) map[string]devModel {
	if url == "" {
		url = ModelsDevURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	var payload struct {
		Go struct {
			Models map[string]devModel `json:"models"`
		} `json:"opencode-go"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, maxModelsDevBody)).Decode(&payload) != nil {
		return nil
	}
	return payload.Go.Models
}

// buildDiscovered turns live ids and metadata into overlay models, skipping ids
// the baseline catalog already covers.
func buildDiscovered(liveIDs []string, dev map[string]devModel) []Discovered {
	var out []Discovered
	for _, id := range liveIDs {
		slug := stripPrefix(id)
		full := idPrefix + slug
		if _, ok := baselineFor(full); ok {
			continue
		}
		meta := dev[slug]
		out = append(out, Discovered{
			ID:              full,
			Name:            cmp.Or(meta.Name, slug),
			Slug:            slug,
			ContextWindow:   positiveOr(meta.Limit.Context, defaultContext),
			MaxOutputTokens: positiveOr(meta.Limit.Output, defaultMaxTokens),
			InPerM:          meta.Cost.Input,
			OutPerM:         meta.Cost.Output,
			Dialect:         inferDialect(slug),
		})
	}
	slices.SortFunc(out, func(a, b Discovered) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// positiveOr returns n when it is positive, else the fallback.
func positiveOr(n, fallback int) int {
	if n > 0 {
		return n
	}
	return fallback
}

// readCache reads the overlay file.
func readCache(path string) (cacheFile, error) {
	var cache cacheFile
	data, err := os.ReadFile(path)
	if err != nil {
		return cache, err
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return cache, fmt.Errorf("opencodego: decode model cache: %w", err)
	}
	return cache, nil
}

// saveCache writes the overlay atomically: a reader, or a second Nova process
// refreshing at the same time, never sees a half-written file.
func saveCache(path string, models []Discovered) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(cacheFile{CheckedAt: time.Now().Unix(), Models: models})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}
