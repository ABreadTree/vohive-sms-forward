package archive

import "time"

type HealthStatus struct {
	LastSuccess       time.Time
	LastAttempt       time.Time
	LastErrorCategory string
}

func (a *Archiver) Health() HealthStatus {
	a.healthMu.RLock()
	defer a.healthMu.RUnlock()
	return a.health
}
