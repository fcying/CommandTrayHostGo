package app

import "github.com/fcying/CommandTrayHostGo/internal/config"

func SameLaunchIdentity(a, b config.EntryConfig) bool {
	return a.SameLaunchIdentity(b)
}

// CanKeepManagedProcess reports whether a running managed process can survive a config reload.
func CanKeepManagedProcess(oldEntry, newEntry config.EntryConfig) bool {
	return SameLaunchIdentity(oldEntry, newEntry) && (newEntry.IsGUI || oldEntry.Name == newEntry.Name)
}

// MatchReloadEntries returns the old entry index for each new entry. Each old
// entry is used at most once.
func MatchReloadEntries(oldEntries, newEntries []config.EntryConfig) []int {
	matches := make([]int, len(newEntries))
	used := make([]bool, len(oldEntries))
	for i := range matches {
		matches[i] = -1
	}
	for i := range newEntries {
		if i < len(oldEntries) && oldEntries[i].Name == newEntries[i].Name && SameLaunchIdentity(oldEntries[i], newEntries[i]) {
			matches[i] = i
			used[i] = true
		}
	}
	for i := range newEntries {
		if matches[i] >= 0 {
			continue
		}
		for j := range oldEntries {
			if !used[j] && oldEntries[j].Name == newEntries[i].Name && SameLaunchIdentity(oldEntries[j], newEntries[i]) {
				matches[i] = j
				used[j] = true
				break
			}
		}
	}
	for i := range newEntries {
		if matches[i] >= 0 {
			continue
		}
		for j := range oldEntries {
			if !used[j] && SameLaunchIdentity(oldEntries[j], newEntries[i]) {
				matches[i] = j
				used[j] = true
				break
			}
		}
	}
	for i := range newEntries {
		if matches[i] >= 0 {
			continue
		}
		for j := range oldEntries {
			if !used[j] && oldEntries[j].Name == newEntries[i].Name {
				matches[i] = j
				used[j] = true
				break
			}
		}
	}
	return matches
}

// PreserveReloadProcessState carries live process state across a config reload
// without overwriting the enabled value resolved from the candidate config and cache.
func PreserveReloadProcessState(candidate, current EntryState) EntryState {
	candidate.Running = current.Running
	candidate.Show = current.Show
	candidate.Generation = current.Generation + 1
	return candidate
}
