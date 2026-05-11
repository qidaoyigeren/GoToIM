package snowflake

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNew_InvalidMachineID(t *testing.T) {
	_, err := New(-1)
	assert.ErrorIs(t, err, ErrInvalidMachine)
	_, err = New(maxMachineID + 1)
	assert.ErrorIs(t, err, ErrInvalidMachine)
}

func TestNew_ValidMachineID(t *testing.T) {
	sf, err := New(0)
	assert.NoError(t, err)
	assert.NotNil(t, sf)
	sf, err = New(1023)
	assert.NoError(t, err)
	assert.NotNil(t, sf)
}

func TestGenerate_Monotonic(t *testing.T) {
	sf, err := New(1)
	assert.NoError(t, err)

	var prev int64
	for i := 0; i < 1000; i++ {
		id, err := sf.Generate()
		assert.NoError(t, err)
		assert.Greater(t, id, prev, "IDs must be monotonically increasing")
		prev = id
	}
}

func TestGenerate_UniqueAcrossCalls(t *testing.T) {
	sf, err := New(42)
	assert.NoError(t, err)

	seen := make(map[int64]struct{}, 10000)
	for i := 0; i < 10000; i++ {
		id, err := sf.Generate()
		assert.NoError(t, err)
		_, exists := seen[id]
		assert.False(t, exists, "duplicate ID: %d", id)
		seen[id] = struct{}{}
	}
}

func TestGenerate_ConcurrentSafety(t *testing.T) {
	sf, err := New(100)
	assert.NoError(t, err)

	const goroutines = 10
	const perGoroutine = 1000
	ids := make(chan int64, goroutines*perGoroutine)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				id, err := sf.Generate()
				assert.NoError(t, err)
				ids <- id
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[int64]struct{}, goroutines*perGoroutine)
	for id := range ids {
		_, exists := seen[id]
		assert.False(t, exists, "duplicate ID in concurrent test: %d", id)
		seen[id] = struct{}{}
	}
	assert.Equal(t, goroutines*perGoroutine, len(seen))
}

func TestGenerate_MachineIDInBits(t *testing.T) {
	machineID := int64(5)
	sf, err := New(machineID)
	assert.NoError(t, err)

	id, err := sf.Generate()
	assert.NoError(t, err)

	extracted := (id >> machineShift) & maxMachineID
	assert.Equal(t, machineID, extracted)
}

func TestGenerateString(t *testing.T) {
	sf, err := New(1)
	assert.NoError(t, err)

	s, err := sf.GenerateString()
	assert.NoError(t, err)
	assert.NotEmpty(t, s)
	assert.NotEqual(t, "0", s)
}

func TestGenerate_DifferentMachineIDs(t *testing.T) {
	sf1, _ := New(1)
	sf2, _ := New(2)

	id1, _ := sf1.Generate()
	id2, _ := sf2.Generate()

	// Different machine IDs should produce different IDs even at same time
	assert.NotEqual(t, id1, id2)
}
