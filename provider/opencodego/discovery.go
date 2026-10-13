package opencodego

import "github.com/AutumnsGrove/nova/provider"

// discovered is the live model overlay, populated by Refresh and the on-disk
// cache. It is empty until discovery runs.
var discovered []provider.Model

// discoveredModels returns the live overlay.
func discoveredModels() []provider.Model { return discovered }
