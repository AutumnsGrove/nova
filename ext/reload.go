package ext

import (
	"context"
	"fmt"
	"path/filepath"
)

// Reload stops every extension, re-reads manifests, rebuilds changed ones, and
// starts them again. A failure in one leaves the others running.
func (h *Host) Reload(ctx context.Context) []error {
	h.life.Lock()
	defer h.life.Unlock()
	for _, inst := range h.snapshot() {
		inst.stop(ctx)
		h.clearRegistrations(inst)
	}
	h.mu.Lock()
	h.insts = nil
	h.byName = map[string]*instance{}
	h.complete = map[string]int{}
	h.mu.Unlock()
	errs := h.rebuildChanged(ctx)
	return append(errs, h.start(ctx)...)
}

// rebuildChanged runs the build step of every extension whose manifest hash
// changed since the previous load, and returns the builds that failed. A failed
// build's hash is not remembered, so the next reload tries it again.
func (h *Host) rebuildChanged(ctx context.Context) []error {
	h.mu.Lock()
	old := h.manifestHashes
	h.manifestHashes = map[string]string{}
	h.mu.Unlock()
	var errs []error
	for _, dir := range h.opt.Dirs {
		key, hash := hashKey(dir), ManifestHash(dir)
		if prev, ok := old[key]; !ok || prev != hash {
			// A manifest that no longer loads is left for start to report.
			if m, err := LoadManifest(dir); err == nil && len(m.Build) > 0 {
				if err := runBuild(ctx, h.opt.Run, dir, m.Build, defaultBuildTimeout); err != nil {
					errs = append(errs, fmt.Errorf("ext: rebuild %s: %w", m.Name, err))
					continue
				}
			}
		}
		h.mu.Lock()
		h.manifestHashes[key] = hash
		h.mu.Unlock()
	}
	return errs
}

// hashKey is the map key for a manifest hash: the absolute directory.
func hashKey(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

// recordManifestHashes remembers the manifest hash of every configured dir.
func (h *Host) recordManifestHashes() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, dir := range h.opt.Dirs {
		h.manifestHashes[hashKey(dir)] = ManifestHash(dir)
	}
}
