package rotatelogs

func (rl *RotateLogs) Sync() error {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	if rl.outFh == nil {
		return nil
	}

	return rl.outFh.Sync()
}
