package providerauth

import (
	"context"
	"errors"
	"io"

	"github.com/AutumnsGrove/nova/auth"
	"github.com/AutumnsGrove/nova/auth/opencode"
	"github.com/AutumnsGrove/nova/provider/opencodego"
)

func init() { Register(openCodeGoProvider()) }

// openCodeGoProvider describes OpenCode Go: a device-code flow or a pasted key,
// stored in the 0600 file the CLI login writes.
func openCodeGoProvider() Provider {
	return Provider{
		ID:           "opencode-go",
		Name:         "OpenCode Go",
		Methods:      []Method{Device, APIKey},
		EnvVars:      []string{auth.EnvOpenCodeGoKey},
		DefaultModel: opencodego.DefaultModel,
		Begin: map[Method]BeginFunc{
			Device: beginOpenCodeGoDevice,
			APIKey: func(context.Context, Config) (Session, error) {
				return newKeySession("Paste your OpenCode Go key"), nil
			},
		},
		Save:   saveOpenCodeGo,
		Load:   loadOpenCodeGo,
		Delete: deleteOpenCodeGo,
	}
}

// beginOpenCodeGoDevice asks the Console for a device and user code.
func beginOpenCodeGoDevice(ctx context.Context, cfg Config) (Session, error) {
	server := cfg.getenv("OPENCODE_CONSOLE_SERVER")
	device, err := opencode.RequestDeviceCode(ctx, opencode.Config{Server: server, Out: io.Discard, OpenBrowser: cfg.OpenBrowser})
	if err != nil {
		return nil, err
	}
	return &deviceSession{device: device, server: server}, nil
}

// deviceSession adapts the console device flow to the registry's Session.
type deviceSession struct {
	device opencode.DeviceCode
	server string
}

func (s *deviceSession) Prompt() string { return "Sign in with OpenCode Go" }
func (s *deviceSession) URL() string    { return s.device.VerificationURL }
func (s *deviceSession) Code() string   { return s.device.UserCode }
func (s *deviceSession) Close() error   { return nil }

func (s *deviceSession) Submit(string) error {
	return errors.New("opencode-go: this flow has no redirect URL to paste")
}

func (s *deviceSession) Wait(ctx context.Context) (Credential, error) {
	key, err := opencode.Complete(ctx, opencode.Config{Server: s.server, Out: io.Discard}, s.device)
	if err != nil {
		return Credential{}, err
	}
	return Credential{Method: Device, Key: key}, nil
}

func saveOpenCodeGo(home string, c Credential) (string, error) {
	path := opencode.DefaultKeyPath(home)
	if err := opencode.SaveKey(path, c.Key); err != nil {
		return "", err
	}
	return path, nil
}

func loadOpenCodeGo(home string) (Credential, bool, error) {
	key, err := opencode.LoadKey(opencode.DefaultKeyPath(home))
	if err != nil {
		if errors.Is(err, auth.ErrNoKey) {
			return Credential{}, false, nil
		}
		return Credential{}, false, err
	}
	return Credential{Method: Device, Key: key}, true, nil
}

func deleteOpenCodeGo(home string) error {
	return opencode.DeleteKey(opencode.DefaultKeyPath(home))
}
