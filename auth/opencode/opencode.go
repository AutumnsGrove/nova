// Package opencode signs in to the OpenCode Console with the OAuth device
// grant and provisions a per-machine OpenCode Go API key. Go inference accepts
// a workspace service key, not a Console session, so signing in means: run the
// device flow, then mint a key for a service account and return it.
package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Defaults for the Console device flow.
const (
	// DefaultServer is the OpenCode Console origin.
	DefaultServer = "https://opencode.ai/console"
	// ClientID is the public client OpenCode itself uses for the device flow.
	ClientID = "opencode-cli"
	// ServiceAccount is the service account that owns the provisioned keys.
	ServiceAccount = "nova"
	// deviceGrant is the OAuth grant type.
	deviceGrant = "urn:ietf:params:oauth:grant-type:device_code"
	// consoleAlias is remapped to opencode.ai in verification URLs.
	consoleAlias = "console.opencode.ai"
)

// Config configures a login.
type Config struct {
	Server         string
	ClientID       string
	ServiceAccount string
	KeyName        string
	HTTPClient     *http.Client
	OpenBrowser    func(string) error
	Out            io.Writer
	// Sleep waits between polls; nil uses a real timer.
	Sleep func(ctx context.Context, d time.Duration) error
}

// DeviceCode is the pending device authorization.
type DeviceCode struct {
	DeviceCode      string
	UserCode        string
	VerificationURL string
	ExpiresAt       time.Time
	Interval        time.Duration
	Server          string
}

// Tokens is the Console session.
type Tokens struct {
	Access  string
	Refresh string
	OrgID   string
}

// Login runs the full flow and returns the provisioned API key. It is
// RequestDeviceCode, print, open, Complete.
func Login(ctx context.Context, cfg Config) (string, error) {
	cfg = withDefaults(cfg)
	device, err := RequestDeviceCode(ctx, cfg)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(cfg.Out, "Open %s and enter code %s\n", device.VerificationURL, device.UserCode)
	if cfg.OpenBrowser != nil {
		_ = cfg.OpenBrowser(device.VerificationURL)
	}
	return Complete(ctx, cfg, device)
}

// Complete finishes a device login after RequestDeviceCode returned: it polls
// for the token, resolves the org, and provisions the API key. It is the
// blocking half a UI can run in the background while it shows the code.
func Complete(ctx context.Context, cfg Config, device DeviceCode) (string, error) {
	cfg = withDefaults(cfg)
	tokens, err := PollToken(ctx, cfg, device)
	if err != nil {
		return "", err
	}
	orgID, err := fetchOrgID(ctx, cfg, tokens)
	if err != nil {
		return "", err
	}
	tokens.OrgID = orgID
	return ProvisionKey(ctx, cfg, tokens)
}

// withDefaults fills the zero config.
func withDefaults(cfg Config) Config {
	if cfg.Server == "" {
		cfg.Server = DefaultServer
	}
	cfg.Server = strings.TrimRight(cfg.Server, "/")
	if cfg.ClientID == "" {
		cfg.ClientID = ClientID
	}
	if cfg.ServiceAccount == "" {
		cfg.ServiceAccount = ServiceAccount
	}
	if cfg.KeyName == "" {
		cfg.KeyName = DefaultKeyName()
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if cfg.Out == nil {
		cfg.Out = os.Stderr
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepCtx
	}
	return cfg
}

// DefaultKeyName is the per-machine key name, so re-login replaces its own key.
func DefaultKeyName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "cli"
	}
	host = strings.SplitN(host, ".", 2)[0]
	host = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, host)
	name := "nova-" + host
	if len(name) > 60 {
		name = name[:60]
	}
	return name
}

// RequestDeviceCode asks the Console for a device and user code.
func RequestDeviceCode(ctx context.Context, cfg Config) (DeviceCode, error) {
	body, _ := json.Marshal(map[string]string{"client_id": cfg.ClientID})
	var payload struct {
		DeviceCode           string `json:"device_code"`
		UserCode             string `json:"user_code"`
		VerificationURI      string `json:"verification_uri"`
		VerificationURICompl string `json:"verification_uri_complete"`
		ExpiresIn            int    `json:"expires_in"`
		Interval             int    `json:"interval"`
	}
	if err := postJSON(ctx, cfg, cfg.Server+"/auth/device/code", "", body, &payload); err != nil {
		return DeviceCode{}, err
	}
	if payload.DeviceCode == "" || payload.UserCode == "" {
		return DeviceCode{}, fmt.Errorf("opencode: incomplete device-code response")
	}
	verification := payload.VerificationURICompl
	if verification == "" {
		verification = payload.VerificationURI
	}
	expires := payload.ExpiresIn
	if expires <= 0 {
		expires = 600
	}
	interval := payload.Interval
	if interval <= 0 {
		interval = 5
	}
	verify, err := resolveVerificationURL(cfg.Server, verification)
	if err != nil {
		return DeviceCode{}, err
	}
	return DeviceCode{
		DeviceCode:      payload.DeviceCode,
		UserCode:        payload.UserCode,
		VerificationURL: verify,
		ExpiresAt:       time.Now().Add(time.Duration(expires) * time.Second),
		Interval:        time.Duration(interval) * time.Second,
		Server:          cfg.Server,
	}, nil
}

// resolveVerificationURL resolves an origin-absolute verification path against
// the server's origin, mapping the console alias to opencode.ai.
func resolveVerificationURL(server, verification string) (string, error) {
	base, err := url.Parse(server)
	if err != nil {
		return "", fmt.Errorf("opencode: bad server URL: %w", err)
	}
	ref, err := url.Parse(verification)
	if err != nil {
		return "", fmt.Errorf("opencode: bad verification URL: %w", err)
	}
	resolved := base.ResolveReference(ref)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", fmt.Errorf("opencode: non-HTTP verification URL")
	}
	if resolved.Hostname() == consoleAlias {
		resolved.Host = strings.Replace(resolved.Host, consoleAlias, "opencode.ai", 1)
	}
	return resolved.String(), nil
}

// PollToken polls the token endpoint until approval, denial, or expiry.
func PollToken(ctx context.Context, cfg Config, device DeviceCode) (Tokens, error) {
	interval := device.Interval
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	for time.Now().Before(device.ExpiresAt) {
		if err := sleep(ctx, interval); err != nil {
			return Tokens{}, err
		}
		body, _ := json.Marshal(map[string]string{
			"grant_type":  deviceGrant,
			"device_code": device.DeviceCode,
			"client_id":   cfg.ClientID,
		})
		var payload struct {
			AccessToken string `json:"access_token"`
			Refresh     string `json:"refresh_token"`
			OrgID       string `json:"org_id"`
			Error       string `json:"error"`
		}
		err := postJSON(ctx, cfg, device.Server+"/auth/device/token", "", body, &payload)
		if err != nil {
			return Tokens{}, err
		}
		switch payload.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "expired_token":
			return Tokens{}, fmt.Errorf("opencode: device code expired")
		case "access_denied":
			return Tokens{}, fmt.Errorf("opencode: sign-in denied")
		case "":
		default:
			return Tokens{}, fmt.Errorf("opencode: sign-in failed: %s", payload.Error)
		}
		if payload.AccessToken == "" {
			return Tokens{}, fmt.Errorf("opencode: token exchange returned no token")
		}
		return Tokens{Access: payload.AccessToken, Refresh: payload.Refresh, OrgID: payload.OrgID}, nil
	}
	return Tokens{}, fmt.Errorf("opencode: device code expired")
}

// fetchOrgID reads the first org the signed-in account belongs to.
func fetchOrgID(ctx context.Context, cfg Config, tokens Tokens) (string, error) {
	var orgs []struct {
		ID string `json:"id"`
	}
	if err := getJSON(ctx, cfg, cfg.Server+"/api/orgs", tokens.Access, "", &orgs); err != nil {
		return "", err
	}
	if len(orgs) > 0 {
		return orgs[0].ID, nil
	}
	return "", nil
}

// ProvisionKey ensures the service account exists and mints a fresh key for it.
func ProvisionKey(ctx context.Context, cfg Config, tokens Tokens) (string, error) {
	accountID, err := ensureServiceAccount(ctx, cfg, tokens)
	if err != nil {
		return "", err
	}
	// Revoke an active key with the same per-machine name first.
	var detail struct {
		Keys []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"keys"`
	}
	if err := getJSON(ctx, cfg, cfg.Server+"/api/service-accounts/"+accountID, tokens.Access, tokens.OrgID, &detail); err == nil {
		for _, key := range detail.Keys {
			if key.Name == cfg.KeyName && (key.Status == "" || key.Status == "active") {
				body, _ := json.Marshal(map[string]any{})
				_ = postJSON(ctx, cfg, cfg.Server+"/api/service-accounts/keys/"+key.ID+"/revoke", tokens.Access, body, nil)
			}
		}
	}
	body, _ := json.Marshal(map[string]string{"name": cfg.KeyName, "permissions": "all"})
	var created struct {
		Token string `json:"token"`
	}
	if err := postJSON(ctx, cfg, cfg.Server+"/api/service-accounts/"+accountID+"/keys", tokens.Access, body, &created); err != nil {
		return "", err
	}
	if created.Token == "" {
		return "", fmt.Errorf("opencode: provisioning returned no key")
	}
	return created.Token, nil
}

// ensureServiceAccount returns the service account id, creating it if needed.
func ensureServiceAccount(ctx context.Context, cfg Config, tokens Tokens) (string, error) {
	var list struct {
		Items []struct {
			Account struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"account"`
		} `json:"items"`
	}
	if err := getJSON(ctx, cfg, cfg.Server+"/api/service-accounts", tokens.Access, tokens.OrgID, &list); err != nil {
		return "", err
	}
	for _, item := range list.Items {
		if item.Account.Name == cfg.ServiceAccount && item.Account.ID != "" {
			return item.Account.ID, nil
		}
	}
	body, _ := json.Marshal(map[string]string{"name": cfg.ServiceAccount})
	var created struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		ID string `json:"id"`
	}
	if err := postJSON(ctx, cfg, cfg.Server+"/api/service-accounts", tokens.Access, body, &created); err != nil {
		return "", err
	}
	if created.Account.ID != "" {
		return created.Account.ID, nil
	}
	if created.ID != "" {
		return created.ID, nil
	}
	return "", fmt.Errorf("opencode: could not create the service account")
}

// getJSON performs an authenticated GET.
func getJSON(ctx context.Context, cfg Config, url, token, orgID string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if orgID != "" {
		req.Header.Set("x-org-id", orgID)
	}
	return doJSON(cfg, req, out)
}

// postJSON performs a JSON POST.
func postJSON(ctx context.Context, cfg Config, url, token string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return doJSON(cfg, req, out)
}

// doJSON sends req and decodes a JSON body into out when out is non-nil.
func doJSON(cfg Config, req *http.Request, out any) error {
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("opencode: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("opencode: %s returned %s", req.URL.Path, resp.Status)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("opencode: decode %s: %w", req.URL.Path, err)
	}
	return nil
}

// sleepCtx waits for d or the context.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
