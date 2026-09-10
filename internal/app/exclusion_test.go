package app

import (
	"reflect"
	"testing"

	"github.com/fcying/CommandTrayHostGo/internal/config"
)

func TestExclusionConflicts(t *testing.T) {
	one := int64(1)
	two := int64(2)
	configs := []config.EntryConfig{
		{ExclusionID: &one},
		{ExclusionID: &one},
		{ExclusionID: &one, IgnoreAll: true},
		{ExclusionID: &two},
		{ExclusionID: &one, NotHosted: true},
	}
	states := []EntryState{
		{},
		{Running: true},
		{Running: true},
		{Running: true},
		{Running: true},
	}
	if got, want := ExclusionConflicts(configs, states, 0), []int{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ExclusionConflicts() = %v, want %v", got, want)
	}
}

func TestExclusionConflictsWithoutID(t *testing.T) {
	if got := ExclusionConflicts([]config.EntryConfig{{}}, []EntryState{{Running: true}}, 0); got != nil {
		t.Fatalf("ExclusionConflicts() = %v, want nil", got)
	}
}
