package ext

import (
	"context"
	"path/filepath"
)

// Reload stops every extension, re-reads manifests, rebuilds changed ones, and
// starts them again. A failure in one leaves the others running.
func (h *Host) Reload(ctx context.Context) []error {
	h.mu.Lock()
	insts := append([]*instance(nil), h.insts...)
	h.mu.Unlock()
	for _, inst := range insts {
		inst.stop(ctx)
		h.clearRegistrations(inst)
	}
	h.mu.Lock()
	h.insts = nil
	h.byName = map[string]*instance{}
	h.complete = map[string]int{}
	h.mu.Unlock()
	// Rebuild extensions whose manifest changed since the last load.
	h.rebuildChanged(ctx)
	return h.Start(ctx)
}

// rebuildChanged runs the build step for extensions whose manifest hash
// changed since the previous load.
func (h *Host) rebuildChanged(ctx context.Context) {
	h.mu.Lock()
	old := h.manifestHashes
	h.manifestHashes = map[string]string{}
	h.mu.Unlock()
	for _, dir := range h.opt.Dirs {
		hash := manifestHash(dir)
		if prev, ok := old[hashKey(dir)]; ok && prev == hash {
			continue
		}
		m, err := LoadManifest(dir)
		if err != nil || len(m.Build) == 0 {
			continue
		}
		run := h.opt.Run
		if run == nil {
			run = execRunner{}
		}
		_ = run.Run(ctx, dir, m.Build)
		h.mu.Lock()
		h.manifestHashes[hashKey(dir)] = hash
		h.mu.Unlock()
	}
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
	if h.manifestHashes == nil {
		h.manifestHashes = map[string]string{}
	}
	for _, dir := range h.opt.Dirs {
		h.manifestHashes[hashKey(dir)] = manifestHash(dir)
	}
}
