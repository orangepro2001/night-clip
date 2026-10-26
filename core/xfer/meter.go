package xfer

import (
	"sync"
	"time"
)

// meter turns a stream of byte counts into throttled progress events and a
// smoothed rate.
type meter struct {
	mu       sync.Mutex
	lastEmit time.Time
	lastAt   time.Time
	lastOff  int64
	rate     float64 // bytes per second, exponentially smoothed
}

func (s *Service) meterFor(id string) *meter {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.rates[id]
	if m == nil {
		m = &meter{}
		s.rates[id] = m
	}
	return m
}

func (s *Service) rate(id string) int64 {
	s.mu.Lock()
	m := s.rates[id]
	s.mu.Unlock()
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(m.rate)
}

func (s *Service) forgetMeter(id string) {
	s.mu.Lock()
	delete(s.rates, id)
	s.mu.Unlock()
}

// progress records that `offset` bytes are done and emits file.progress at most
// once per ProgressEvery (always for the final byte).
func (s *Service) progress(id string, offset, size int64) {
	m := s.meterFor(id)
	now := time.Now()
	m.mu.Lock()
	if m.lastAt.IsZero() {
		m.lastAt, m.lastOff = now, offset
	}
	emit := now.Sub(m.lastEmit) >= s.timing.ProgressEvery || offset >= size
	if emit {
		if dt := now.Sub(m.lastAt).Seconds(); dt > 0 && offset >= m.lastOff {
			inst := float64(offset-m.lastOff) / dt
			if m.rate == 0 {
				m.rate = inst
			} else {
				m.rate = 0.7*m.rate + 0.3*inst
			}
		}
		m.lastAt, m.lastOff, m.lastEmit = now, offset, now
	}
	rate := int64(m.rate)
	m.mu.Unlock()
	if emit {
		_ = s.db.SetTransferOffset(id, offset)
		s.emit(map[string]any{"type": "file.progress", "transfer_id": id, "offset": offset, "size": size, "rate_bps": rate})
	}
}
