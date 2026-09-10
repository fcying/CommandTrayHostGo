package app

import (
	"testing"

	"github.com/fcying/CommandTrayHostGo/internal/config"
)

func TestOwnershipFor(t *testing.T) {
	tests := []struct {
		name  string
		entry config.EntryConfig
		want  Ownership
	}{
		{name: "managed", want: ManagedAndJobOwned},
		{name: "detached", entry: config.EntryConfig{NotHosted: true}, want: FullyDetached},
		{name: "job owned unmanaged", entry: config.EntryConfig{NotMonitored: true}, want: UnmanagedButJobOwned},
		{name: "not monitored wins", entry: config.EntryConfig{NotHosted: true, NotMonitored: true}, want: UnmanagedButJobOwned},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := OwnershipFor(tc.entry); got != tc.want {
				t.Fatalf("OwnershipFor() = %d, want %d", got, tc.want)
			}
		})
	}
}
