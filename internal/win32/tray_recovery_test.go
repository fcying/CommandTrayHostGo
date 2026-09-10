package win32

import (
	"errors"
	"testing"
)

func TestTrayIconRecoveryKeepsExistingIcon(t *testing.T) {
	var recovery trayIconRecovery
	addCalled := false

	retry, err := recovery.attempt(
		func() bool { return true },
		func() error {
			addCalled = true
			return nil
		},
	)
	if err != nil {
		t.Fatalf("attempt() error = %v", err)
	}
	if retry {
		t.Fatal("attempt() requested retry for an existing icon")
	}
	if addCalled {
		t.Fatal("attempt() added an icon that could be modified")
	}
}

func TestTrayIconRecoveryAddsMissingIcon(t *testing.T) {
	var recovery trayIconRecovery
	addCalled := false

	retry, err := recovery.attempt(
		func() bool { return false },
		func() error {
			addCalled = true
			return nil
		},
	)
	if err != nil {
		t.Fatalf("attempt() error = %v", err)
	}
	if retry {
		t.Fatal("attempt() requested retry after adding the icon")
	}
	if !addCalled {
		t.Fatal("attempt() did not add a missing icon")
	}
}

func TestTrayIconRecoveryRetriesTransientFailure(t *testing.T) {
	var recovery trayIconRecovery
	unavailable := errors.New("notification area unavailable")

	for attempt := 1; attempt <= trayIconRetryLimit; attempt++ {
		retry, err := recovery.attempt(
			func() bool { return false },
			func() error { return unavailable },
		)
		if !errors.Is(err, unavailable) {
			t.Fatalf("attempt %d error = %v", attempt, err)
		}
		if want := attempt < trayIconRetryLimit; retry != want {
			t.Fatalf("attempt %d retry = %v, want %v", attempt, retry, want)
		}
	}

	retry, err := recovery.attempt(
		func() bool { return false },
		func() error { return unavailable },
	)
	if !errors.Is(err, unavailable) {
		t.Fatalf("attempt after limit error = %v", err)
	}
	if !retry {
		t.Fatal("attempt after limit did not start a new retry sequence")
	}
}
