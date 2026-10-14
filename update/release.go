package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// runRelease resolves, downloads, verifies, and installs a GitHub release.
func runRelease(ctx context.Context, opt Options) (Result, error) {
	if !supported(opt.GOOS, opt.GOARCH) {
		return Result{}, fmt.Errorf("unsupported platform %s/%s: releases are made for linux and darwin on amd64 and arm64", opt.GOOS, opt.GOARCH)
	}
	version := opt.Version
	if version == "" {
		resolved, err := latestVersion(ctx, opt)
		if err != nil {
			return Result{}, err
		}
		version = resolved
	}
	res := Result{Mode: Release, From: opt.Current, To: version}
	if opt.Check {
		if version == opt.Current {
			res.Message = fmt.Sprintf("nova %s is the latest release", version)
		} else {
			res.Message = fmt.Sprintf("nova %s is available (current %s)", version, displayVersion(opt.Current))
		}
		return res, nil
	}
	if version == opt.Current && !opt.Force {
		res.Message = fmt.Sprintf("nova %s is already the latest release", version)
		return res, nil
	}
	prefix := "nova"
	if opt.Flavour == "min" {
		prefix = "nova-min"
	}
	archive := fmt.Sprintf("%s_%s_%s_%s.tar.gz", prefix, version, opt.GOOS, opt.GOARCH)
	base := fmt.Sprintf("%s/releases/download/%s", opt.BaseURL, version)
	body, err := download(ctx, opt, base+"/"+archive)
	if err != nil {
		return Result{}, err
	}
	checksums, err := download(ctx, opt, base+"/checksums.txt")
	if err != nil {
		return Result{}, err
	}
	want, ok := checksumFor(checksums, archive)
	if !ok {
		return Result{}, fmt.Errorf("checksums.txt has no entry for %s", archive)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != want {
		return Result{}, fmt.Errorf("checksum mismatch for %s", archive)
	}
	staged, err := stage(opt.Target)
	if err != nil {
		return Result{}, err
	}
	defer staged.cleanup()
	if err := extract(staged.path, body, prefix); err != nil {
		return Result{}, err
	}
	if err := staged.commit(ctx, opt); err != nil {
		return Result{}, err
	}
	res.Changed = true
	res.Message = fmt.Sprintf("updated nova %s -> %s", displayVersion(opt.Current), version)
	return res, nil
}

// supported reports whether a release exists for the platform.
func supported(goos, goarch string) bool {
	if goos != "linux" && goos != "darwin" {
		return false
	}
	return goarch == "amd64" || goarch == "arm64"
}

// latestVersion reads the tag from the release API.
func latestVersion(ctx context.Context, opt Options) (string, error) {
	body, err := download(ctx, opt, opt.APIURL)
	if err != nil {
		return "", err
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("release API: %w", err)
	}
	if payload.TagName == "" {
		return "", fmt.Errorf("release API returned no tag_name")
	}
	return payload.TagName, nil
}

// download fetches a URL and returns its body, failing on any non-200 status.
func download(ctx context.Context, opt Options, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := opt.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// checksumFor finds the digest for name in a checksums file of "<hex>  <name>"
// lines. It returns false when no line matches.
func checksumFor(data []byte, name string) (string, bool) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}
