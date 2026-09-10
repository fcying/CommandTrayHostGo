package app

import "github.com/fcying/CommandTrayHostGo/internal/config"

type Ownership uint8

const (
	ManagedAndJobOwned Ownership = iota
	UnmanagedButJobOwned
	FullyDetached
)

func OwnershipFor(entry config.EntryConfig) Ownership {
	if entry.NotMonitored {
		return UnmanagedButJobOwned
	}
	if entry.NotHosted {
		return FullyDetached
	}
	return ManagedAndJobOwned
}

type EntryState struct {
	Enabled    bool
	Running    bool
	Show       bool
	Generation uint64
}

// ExclusionConflicts returns running managed entries that must stop before the
// target can start. ignore_all protects an entry from this bulk stop.
func ExclusionConflicts(configs []config.EntryConfig, states []EntryState, target int) []int {
	if target < 0 || target >= len(configs) || target >= len(states) || configs[target].ExclusionID == nil {
		return nil
	}
	id := *configs[target].ExclusionID
	var conflicts []int
	for i := range configs {
		if i == target || i >= len(states) || !states[i].Running || configs[i].IgnoreAll ||
			configs[i].ExclusionID == nil || *configs[i].ExclusionID != id || OwnershipFor(configs[i]) != ManagedAndJobOwned {
			continue
		}
		conflicts = append(conflicts, i)
	}
	return conflicts
}
