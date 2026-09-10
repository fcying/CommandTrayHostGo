package win32

const trayIconRetryLimit = 20

type trayIconRecovery struct {
	failures uint8
}

func (r *trayIconRecovery) reset() {
	r.failures = 0
}

func (r *trayIconRecovery) attempt(modify func() bool, add func() error) (bool, error) {
	if modify() {
		r.reset()
		return false, nil
	}

	err := add()
	if err == nil {
		r.reset()
		return false, nil
	}

	r.failures++
	if r.failures < trayIconRetryLimit {
		return true, err
	}
	r.reset()
	return false, err
}
