package win32

import "time"

const cronRetryDelay = 250 * time.Millisecond

type cronPhase uint8

const (
	cronDisabled cronPhase = iota
	cronWaiting
	cronActionPending
	cronFinalStopRetryWaiting
	cronFinalStopPending
	cronExhausted
)

func cronFinalStopCompletion(now time.Time, actionErr error) (cronPhase, time.Time) {
	if actionErr != nil {
		return cronFinalStopRetryWaiting, now.Add(cronRetryDelay)
	}
	return cronExhausted, time.Time{}
}
