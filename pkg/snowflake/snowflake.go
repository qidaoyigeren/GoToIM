package snowflake

import (
	"errors"
	"runtime"
	"sync"
	"time"
)

const (
	epoch        = 1704067200000 // 2024-01-01T00:00:00Z in milliseconds
	machineBits  = 10
	sequenceBits = 12
	maxMachineID = (1 << machineBits) - 1     // 1023
	maxSequence  = (1 << sequenceBits) - 1    // 4095
	machineShift = sequenceBits               // 12
	timeShift    = sequenceBits + machineBits // 22
)

var (
	ErrClockMovedBack = errors.New("snowflake: clock moved backwards")
	ErrInvalidMachine = errors.New("snowflake: invalid machine ID")
)

// Snowflake generates unique 63-bit IDs.
// Layout: 1 sign + 41 timestamp(ms) + 10 machine + 12 sequence = 63 bits
type Snowflake struct {
	mu        sync.Mutex
	machineID int64
	sequence  int64
	lastTime  int64
}

// New creates a new Snowflake generator. machineID must be in [0, 1023].
func New(machineID int64) (*Snowflake, error) {
	if machineID < 0 || machineID > maxMachineID {
		return nil, ErrInvalidMachine
	}
	return &Snowflake{machineID: machineID}, nil
}

// Generate returns a unique int64 ID.
func (s *Snowflake) Generate() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli() - epoch
	if now < s.lastTime {
		return 0, ErrClockMovedBack
	}

	if now == s.lastTime {
		s.sequence = (s.sequence + 1) & maxSequence
		if s.sequence == 0 {
			// Sequence exhausted in this ms, wait for next ms
			for now <= s.lastTime {
				runtime.Gosched()
				now = time.Now().UnixMilli() - epoch
			}
		}
	} else {
		s.sequence = 0
	}

	s.lastTime = now
	return (now << timeShift) | (s.machineID << machineShift) | s.sequence, nil
}

// GenerateString returns a unique ID as a decimal string.
func (s *Snowflake) GenerateString() (string, error) {
	id, err := s.Generate()
	if err != nil {
		return "", err
	}
	// Fast int64 to string conversion
	return intToString(id), nil
}

func intToString(n int64) string {
	if n == 0 {
		return "0"
	}
	negative := false
	if n < 0 {
		negative = true
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
