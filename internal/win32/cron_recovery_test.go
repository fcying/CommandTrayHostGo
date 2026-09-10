package win32

import (
	"errors"
	"testing"
	"time"
)

func TestCronFinalStopCompletionRetriesFailure(t *testing.T) {
	now := time.Unix(100, 0)
	phase, next := cronFinalStopCompletion(now, errors.New("stop failed"))
	if phase != cronFinalStopRetryWaiting {
		t.Fatalf("phase = %v, want retry waiting", phase)
	}
	if want := now.Add(cronRetryDelay); !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestCronFinalStopCompletionExhaustsAfterSuccess(t *testing.T) {
	phase, next := cronFinalStopCompletion(time.Unix(100, 0), nil)
	if phase != cronExhausted {
		t.Fatalf("phase = %v, want exhausted", phase)
	}
	if !next.IsZero() {
		t.Fatalf("next = %v, want zero", next)
	}
}
