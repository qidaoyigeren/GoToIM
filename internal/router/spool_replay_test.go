package router

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// errMessageDAO wraps mockMessageDAO to inject errors on AddToOfflineQueue.
type errMessageDAO struct {
	*mockMessageDAO
	offlineErr error
}

func (m *errMessageDAO) AddToOfflineQueue(ctx context.Context, uid int64, msgID string, seq float64) error {
	if m.offlineErr != nil {
		return m.offlineErr
	}
	return m.mockMessageDAO.AddToOfflineQueue(ctx, uid, msgID, seq)
}

// writeTestSpoolFile writes a localSpoolRecord to the spool directory for testing.
func writeTestSpoolFile(t *testing.T, dir string, rec localSpoolRecord) string {
	t.Helper()
	data, err := json.Marshal(rec)
	assert.NoError(t, err)
	name := rec.MsgID + ".json"
	path := filepath.Join(dir, name)
	assert.NoError(t, os.WriteFile(path, append(data, '\n'), 0o644))
	return path
}

func TestSpoolReplayWorkerProcessesFiles(t *testing.T) {
	// Setup: create a temp spool directory with a date subdirectory
	tmpDir := t.TempDir()
	dateDir := filepath.Join(tmpDir, time.Now().Format("20060102"))
	assert.NoError(t, os.MkdirAll(dateDir, 0o755))

	// Write a test spool file
	rec := localSpoolRecord{
		MsgID:     "test-msg-001",
		ToUID:     1001,
		Op:        0,
		Body:      []byte("hello"),
		Seq:       1,
		Server:    "comet-1",
		Keys:      []string{"key1"},
		Headers:   map[string]string{"goim_trace_id": "trace-001"},
		Reason:    "test",
		CreatedAt: time.Now().UnixMilli(),
	}
	filePath := writeTestSpoolFile(t, dateDir, rec)

	// Create worker with mock DAO and producer
	msgDAO := newMockMessageDAO()
	prod := &mockProducer{}
	worker := NewSpoolReplayWorker(msgDAO, prod, SpoolReplayConfig{
		SpoolDir:     tmpDir,
		PollInterval: time.Hour, // won't auto-fire
		MaxAge:       time.Hour,
		BatchSize:    10,
	})

	// Execute
	err := worker.ProcessOnce(context.Background())
	assert.NoError(t, err)

	// Verify: file should be deleted
	_, statErr := os.Stat(filePath)
	assert.True(t, os.IsNotExist(statErr), "spool file should be deleted after successful replay")

	// Verify: DAO received the offline queue write
	// mockMessageDAO uses string(rune(uid)) as the map key
	uidKey := string(rune(1001))
	assert.Len(t, msgDAO.offline[uidKey], 1)
	assert.Equal(t, "test-msg-001", msgDAO.offline[uidKey][0])

	// Verify: producer received the Kafka message
	assert.Len(t, prod.enqueued, 1)
	assert.Equal(t, "1001", prod.enqueued[0].Key)
}

func TestSpoolReplayWorkerSkipsExpired(t *testing.T) {
	tmpDir := t.TempDir()
	dateDir := filepath.Join(tmpDir, time.Now().Format("20060102"))
	assert.NoError(t, os.MkdirAll(dateDir, 0o755))

	// Write a file with old mod time (set via CreatedAt)
	rec := localSpoolRecord{
		MsgID:     "expired-msg-001",
		ToUID:     2001,
		Op:        0,
		Body:      []byte("old"),
		Seq:       1,
		CreatedAt: time.Now().Add(-48 * time.Hour).UnixMilli(),
	}
	filePath := writeTestSpoolFile(t, dateDir, rec)

	// Touch the file to have old mod time
	oldTime := time.Now().Add(-48 * time.Hour)
	os.Chtimes(filePath, oldTime, oldTime)

	msgDAO := newMockMessageDAO()
	prod := &mockProducer{}
	worker := NewSpoolReplayWorker(msgDAO, prod, SpoolReplayConfig{
		SpoolDir:     tmpDir,
		PollInterval: time.Hour,
		MaxAge:       time.Hour, // 1 hour max age, file is 48h old
		BatchSize:    10,
	})

	err := worker.ProcessOnce(context.Background())
	assert.NoError(t, err)

	// Verify: file should be moved to expired/, not deleted
	_, statErr := os.Stat(filePath)
	assert.True(t, os.IsNotExist(statErr), "original file should be gone")

	expiredPath := filepath.Join(dateDir, "expired", "expired-msg-001.json")
	_, statErr = os.Stat(expiredPath)
	assert.NoError(t, statErr, "file should be in expired/ directory")

	// Verify: DAO and producer should NOT have been called
	assert.Empty(t, msgDAO.offline)
	assert.Empty(t, prod.enqueued)
}

func TestSpoolReplayWorkerRetainsOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	dateDir := filepath.Join(tmpDir, time.Now().Format("20060102"))
	assert.NoError(t, os.MkdirAll(dateDir, 0o755))

	rec := localSpoolRecord{
		MsgID:     "fail-msg-001",
		ToUID:     3001,
		Op:        0,
		Body:      []byte("payload"),
		Seq:       1,
		CreatedAt: time.Now().UnixMilli(),
	}
	filePath := writeTestSpoolFile(t, dateDir, rec)

	// Inject errors on both DAO and producer
	msgDAO := &errMessageDAO{
		mockMessageDAO: newMockMessageDAO(),
		offlineErr:     assert.AnError,
	}
	prod := &mockProducer{userErr: assert.AnError}
	worker := NewSpoolReplayWorker(msgDAO, prod, SpoolReplayConfig{
		SpoolDir:     tmpDir,
		PollInterval: time.Hour,
		MaxAge:       time.Hour,
		BatchSize:    10,
	})

	err := worker.ProcessOnce(context.Background())
	assert.NoError(t, err) // ProcessOnce itself doesn't return error for individual failures

	// Verify: file should be RETAINED (not deleted)
	_, statErr := os.Stat(filePath)
	assert.NoError(t, statErr, "spool file should be retained on replay failure")
}

func TestSpoolReplayWorkerIgnoresNonDateDirs(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a non-date directory
	nonDateDir := filepath.Join(tmpDir, "expired")
	assert.NoError(t, os.MkdirAll(nonDateDir, 0o755))

	// Write a file in it
	rec := localSpoolRecord{MsgID: "ignored", ToUID: 1, CreatedAt: time.Now().UnixMilli()}
	data, _ := json.Marshal(rec)
	os.WriteFile(filepath.Join(nonDateDir, "ignored.json"), data, 0o644)

	msgDAO := newMockMessageDAO()
	prod := &mockProducer{}
	worker := NewSpoolReplayWorker(msgDAO, prod, SpoolReplayConfig{
		SpoolDir:     tmpDir,
		PollInterval: time.Hour,
		MaxAge:       time.Hour,
		BatchSize:    10,
	})

	err := worker.ProcessOnce(context.Background())
	assert.NoError(t, err)

	// Verify: nothing was processed
	assert.Empty(t, msgDAO.offline)
	assert.Empty(t, prod.enqueued)
}

func TestSpoolReplayWorkerPartialSuccess(t *testing.T) {
	// When Redis fails but Kafka succeeds, the file should still be deleted
	tmpDir := t.TempDir()
	dateDir := filepath.Join(tmpDir, time.Now().Format("20060102"))
	assert.NoError(t, os.MkdirAll(dateDir, 0o755))

	rec := localSpoolRecord{
		MsgID:     "partial-msg-001",
		ToUID:     4001,
		Op:        0,
		Body:      []byte("body"),
		Seq:       1,
		CreatedAt: time.Now().UnixMilli(),
	}
	filePath := writeTestSpoolFile(t, dateDir, rec)

	// Redis fails, Kafka succeeds
	msgDAO := &errMessageDAO{
		mockMessageDAO: newMockMessageDAO(),
		offlineErr:     assert.AnError,
	}
	prod := &mockProducer{} // succeeds
	worker := NewSpoolReplayWorker(msgDAO, prod, SpoolReplayConfig{
		SpoolDir:     tmpDir,
		PollInterval: time.Hour,
		MaxAge:       time.Hour,
		BatchSize:    10,
	})

	err := worker.ProcessOnce(context.Background())
	assert.NoError(t, err)

	// Verify: file should be deleted (Kafka success is enough)
	_, statErr := os.Stat(filePath)
	assert.True(t, os.IsNotExist(statErr), "file should be deleted when at least one channel succeeds")

	// Verify: Kafka received the message
	assert.Len(t, prod.enqueued, 1)
}

func TestSpoolReplayWorkerEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	// No date subdirectories at all

	msgDAO := newMockMessageDAO()
	prod := &mockProducer{}
	worker := NewSpoolReplayWorker(msgDAO, prod, SpoolReplayConfig{
		SpoolDir:     tmpDir,
		PollInterval: time.Hour,
		MaxAge:       time.Hour,
		BatchSize:    10,
	})

	err := worker.ProcessOnce(context.Background())
	assert.NoError(t, err)
}

func TestSpoolReplayWorkerNonexistentDir(t *testing.T) {
	msgDAO := newMockMessageDAO()
	prod := &mockProducer{}
	worker := NewSpoolReplayWorker(msgDAO, prod, SpoolReplayConfig{
		SpoolDir:     filepath.Join(os.TempDir(), "nonexistent-spool-dir-xyz"),
		PollInterval: time.Hour,
		MaxAge:       time.Hour,
		BatchSize:    10,
	})

	err := worker.ProcessOnce(context.Background())
	assert.NoError(t, err) // should not error on missing dir
}

func TestSpoolReplayWorkerBatchLimit(t *testing.T) {
	tmpDir := t.TempDir()
	dateDir := filepath.Join(tmpDir, time.Now().Format("20060102"))
	assert.NoError(t, os.MkdirAll(dateDir, 0o755))

	// Write 5 files but set batch size to 2
	for i := 0; i < 5; i++ {
		rec := localSpoolRecord{
			MsgID:     fmt.Sprintf("batch-msg-%03d", i),
			ToUID:     int64(5000 + i),
			Op:        0,
			Body:      []byte("body"),
			Seq:       int64(i),
			CreatedAt: time.Now().UnixMilli(),
		}
		writeTestSpoolFile(t, dateDir, rec)
	}

	msgDAO := newMockMessageDAO()
	prod := &mockProducer{}
	worker := NewSpoolReplayWorker(msgDAO, prod, SpoolReplayConfig{
		SpoolDir:     tmpDir,
		PollInterval: time.Hour,
		MaxAge:       time.Hour,
		BatchSize:    2, // only process 2 per round
	})

	// First pass: should process 2
	err := worker.ProcessOnce(context.Background())
	assert.NoError(t, err)
	assert.Len(t, prod.enqueued, 2)

	// Second pass: should process 2 more
	err = worker.ProcessOnce(context.Background())
	assert.NoError(t, err)
	assert.Len(t, prod.enqueued, 4)

	// Third pass: should process the last 1
	err = worker.ProcessOnce(context.Background())
	assert.NoError(t, err)
	assert.Len(t, prod.enqueued, 5)
}
