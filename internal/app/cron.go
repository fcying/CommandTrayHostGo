package app

import "time"

const (
	CronExactTimerMaximum = 2061083 * time.Second
	CronRenewInterval     = 20 * 24 * time.Hour
)

type CronCounter struct {
	Remaining int64
	Finite    bool
}

func NewCronCounter(count int64) CronCounter {
	return CronCounter{Remaining: count, Finite: count > 0}
}

func (c *CronCounter) Consume() bool {
	if !c.Finite || c.Remaining <= 0 {
		return false
	}
	c.Remaining--
	return c.Remaining == 0
}

func CronTimerDelay(now, next time.Time) (time.Duration, bool) {
	delay := next.Sub(now)
	if delay <= 0 {
		return time.Millisecond, false
	}
	if delay > CronExactTimerMaximum {
		return CronRenewInterval, true
	}
	return delay, false
}
