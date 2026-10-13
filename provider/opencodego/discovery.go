package opencodego

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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

// cacheFile is the on-disk overlay.
type cacheFile struct {
	CheckedAt int64        `json:"checked_at"`
	Models    []Discovered `json:"models"`
}

// discovered is the live overlay, populated by Refresh and LoadCache.
var discovered []Discovered

// discoveredModels converts the overlay to provider models.
func discoveredModels() []provider.Model {
	out := make([]provider.Model, 0, len(discovered))
	for _, d := range discovered {
		out = append(out, provider.Model{
			ID:              d.ID,
			Name:            d.Name,
			Provider:        ProviderName,
			Slug:            d.Slug,
			ContextWindow:   d.ContextWindow,
			MaxOutputTokens: d.MaxOutputTokens,
			InPerM:          d.InPerM,
			OutPerM:         d.OutPerM,
		})
	}
	return out
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
	dev := loadModelsDev(cctx, client, cfg.ModelsDevURL)
	models := buildDiscovered(liveIDs, dev)
	discovered = models
	if cfg.CachePath != "" {
		_ = saveCache(cfg.CachePath, models)
	}
	RegisterModels()
	return nil
}

// LoadCache loads the overlay from disk and re-registers the catalog.
func LoadCache(path string) error {
	models, err := readCache(path)
	if err != nil {
		return err
	}
	discovered = models
	RegisterModels()
	return nil
}

// CacheFresh reports whether the cache exists and is younger than maxAge.
func CacheFresh(path string, maxAge time.Duration) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var cache cacheFile
	if json.Unmarshal(data, &cache) != nil {
		return false
	}
	if maxAge <= 0 {
		maxAge = CacheMaxAge
	}
	return time.Since(time.Unix(cache.CheckedAt, 0)) < maxAge
}

// fetchLiveIDs reads the model ids from the Go models endpoint.
func fetchLiveIDs(ctx context.Context, client *http.Client, base, key string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, err
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
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var wrapper struct {
		Data []json.RawMessage `json:"data"`
	}
	var rows []json.RawMessage
	if json.Unmarshal(body, &wrapper) == nil && len(wrapper.Data) > 0 {
		rows = wrapper.Data
	} else {
		_ = json.Unmarshal(body, &rows)
	}
	var ids []string
	for _, row := range rows {
		var id string
		if json.Unmarshal(row, &id) == nil && id != "" {
			ids = append(ids, id)
			continue
		}
		var obj struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(row, &obj) == nil && obj.ID != "" {
			ids = append(ids, obj.ID)
		}
	}
	return ids, nil
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

// loadModelsDev reads the opencode-go metadata; a failure is an empty map.
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
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	var payload struct {
		Go struct {
			Models map[string]devModel `json:"models"`
		} `json:"opencode-go"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return nil
	}
	return payload.Go.Models
}

// buildDiscovered turns live ids and metadata into overlay models, skipping ids
// the baseline catalog already covers.
func buildDiscovered(liveIDs []string, dev map[string]devModel) []Discovered {
	baselineIDs := map[string]bool{}
	for _, b := range baseline {
		baselineIDs[b.id] = true
	}
	var out []Discovered
	for _, id := range liveIDs {
		full := "opencode-go/" + stripPrefix(id)
		if baselineIDs[full] {
			continue
		}
		meta := dev[stripPrefix(id)]
		contextWindow := meta.Limit.Context
		if contextWindow <= 0 {
			contextWindow = defaultContext
		}
		maxOutput := meta.Limit.Output
		if maxOutput <= 0 {
			maxOutput = defaultMaxTokens
		}
		name := meta.Name
		if name == "" {
			name = stripPrefix(id)
		}
		out = append(out, Discovered{
			ID:              full,
			Name:            name,
			Slug:            stripPrefix(id),
			ContextWindow:   contextWindow,
			MaxOutputTokens: maxOutput,
			InPerM:          meta.Cost.Input,
			OutPerM:         meta.Cost.Output,
			Dialect:         inferDialect(stripPrefix(id)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// readCache reads the overlay file.
func readCache(path string) ([]Discovered, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cache cacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	return cache.Models, nil
}

// saveCache writes the overlay atomically.
func saveCache(path string, models []Discovered) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(cacheFile{CheckedAt: time.Now().Unix(), Models: models})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
