package app

import (
	"testing"
	"time"
)

func TestCronCounter(t *testing.T) {
	infinite := NewCronCounter(0)
	if infinite.Consume() || infinite.Remaining != 0 || infinite.Finite {
		t.Fatalf("infinite counter changed: %+v", infinite)
	}

	finite := NewCronCounter(2)
	if finite.Consume() || finite.Remaining != 1 {
		t.Fatalf("first consume = %+v, want one remaining", finite)
	}
	if !finite.Consume() || finite.Remaining != 0 {
		t.Fatalf("second consume = %+v, want exhausted", finite)
	}
	if finite.Consume() || finite.Remaining != 0 {
		t.Fatalf("exhausted counter changed: %+v", finite)
	}
}

func TestCronTimerDelay(t *testing.T) {
	now := time.Unix(100, 0)
	for _, test := range []struct {
		name      string
		delay     time.Duration
		want      time.Duration
		wantRenew bool
	}{
		{name: "due", delay: 0, want: time.Millisecond},
		{name: "maximum", delay: CronExactTimerMaximum, want: CronExactTimerMaximum},
		{name: "renew", delay: CronExactTimerMaximum + time.Second, want: CronRenewInterval, wantRenew: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, renew := CronTimerDelay(now, now.Add(test.delay))
			if got != test.want || renew != test.wantRenew {
				t.Fatalf("CronTimerDelay = (%v, %v), want (%v, %v)", got, renew, test.want, test.wantRenew)
			}
		})
	}
}
