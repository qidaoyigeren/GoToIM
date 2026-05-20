// SpoolReplayWorker periodically scans the local durable spool directory
// and replays messages that were written when both Redis and Kafka were down.
// Successfully replayed files are deleted; expired files are archived.

package router

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/internal/logic/dao"
	"github.com/Terry-Mao/goim/internal/mq"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/Terry-Mao/goim/pkg/metrics"
)

// SpoolReplayConfig controls the spool replay worker behavior.
type SpoolReplayConfig struct {
	// SpoolDir is the root directory for spool files.
	// Must match the directory used by writeLocalSpool.
	SpoolDir string

	// PollInterval is how often the worker scans the spool directory.
	// Default: 30s.
	PollInterval time.Duration

	// MaxAge is the maximum age of a spool file before it's considered expired.
	// Expired files are moved to an "expired/" subdirectory.
	// Default: 24h.
	MaxAge time.Duration

	// BatchSize is the maximum number of files to process per scan.
	// Default: 50.
	BatchSize int
}

// SpoolReplayWorker periodically scans the local durable spool directory
// and attempts to replay messages to Redis offline queue and/or Kafka.
type SpoolReplayWorker struct {
	spoolDir     string
	msgDAO       dao.MessageDAO
	producer     mq.Producer
	pollInterval time.Duration
	maxAge       time.Duration
	batchSize    int
	stopCh       chan struct{}
	wg           sync.WaitGroup
}

// NewSpoolReplayWorker creates a new spool replay worker.
func NewSpoolReplayWorker(msgDAO dao.MessageDAO, producer mq.Producer, cfg SpoolReplayConfig) *SpoolReplayWorker {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 30 * time.Second
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 24 * time.Hour
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.SpoolDir == "" {
		cfg.SpoolDir = defaultSpoolDir()
	}
	return &SpoolReplayWorker{
		spoolDir:     cfg.SpoolDir,
		msgDAO:       msgDAO,
		producer:     producer,
		pollInterval: cfg.PollInterval,
		maxAge:       cfg.MaxAge,
		batchSize:    cfg.BatchSize,
		stopCh:       make(chan struct{}),
	}
}

// Start begins the background replay loop.
func (w *SpoolReplayWorker) Start() {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = w.ProcessOnce(context.Background())
			case <-w.stopCh:
				return
			}
		}
	}()
	log.Infof("spool replay worker started: dir=%s interval=%s maxAge=%s batchSize=%d",
		w.spoolDir, w.pollInterval, w.maxAge, w.batchSize)
}

// Stop gracefully stops the worker, waiting for the current scan to finish.
func (w *SpoolReplayWorker) Stop() {
	close(w.stopCh)
	w.wg.Wait()
	log.Info("spool replay worker stopped")
}

// ProcessOnce performs a single scan-and-replay pass over the spool directory.
// It is public for testing purposes.
func (w *SpoolReplayWorker) ProcessOnce(ctx context.Context) error {
	entries, err := os.ReadDir(w.spoolDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read spool dir: %w", err)
	}

	var totalProcessed, totalFailed, totalExpired int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Only process date-formatted directories (20060102)
		dirName := entry.Name()
		if _, err := time.Parse("20060102", dirName); err != nil {
			continue
		}
		dirPath := filepath.Join(w.spoolDir, dirName)
		processed, failed, expired := w.drainDir(ctx, dirPath)
		totalProcessed += processed
		totalFailed += failed
		totalExpired += expired
	}

	// Update file count gauge
	w.updateFileCount()

	if totalProcessed+totalFailed+totalExpired > 0 {
		log.Infof("spool replay: processed=%d failed=%d expired=%d", totalProcessed, totalFailed, totalExpired)
	}
	return nil
}

// drainDir processes spool files in a single date directory.
func (w *SpoolReplayWorker) drainDir(ctx context.Context, dirPath string) (processed, failed, expired int) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		log.Errorf("spool replay: read dir %s: %v", dirPath, err)
		return
	}

	// Collect .json files only (skip .tmp, expired/, etc.)
	type fileEntry struct {
		name    string
		path    string
		modTime time.Time
	}
	var files []fileEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileEntry{name: e.Name(), path: filepath.Join(dirPath, e.Name()), modTime: info.ModTime()})
	}

	// Sort by modification time (oldest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	// Process up to batchSize files
	limit := w.batchSize
	if limit > len(files) {
		limit = len(files)
	}

	for i := 0; i < limit; i++ {
		f := files[i]
		data, err := os.ReadFile(f.path)
		if err != nil {
			log.Errorf("spool replay: read %s: %v", f.path, err)
			failed++
			continue
		}

		// Check if expired by file age (fast path before JSON parse)
		if time.Since(f.modTime) > w.maxAge {
			w.moveToExpired(dirPath, f.name, data)
			expired++
			metrics.SpoolReplayTotal.WithLabelValues("expired").Inc()
			continue
		}

		if err := w.replayRecord(ctx, data); err != nil {
			log.Warningf("spool replay: failed %s: %v", f.name, err)
			failed++
			metrics.SpoolReplayTotal.WithLabelValues("failed").Inc()
			continue
		}

		// Success — remove the file
		if err := os.Remove(f.path); err != nil {
			log.Errorf("spool replay: remove %s: %v", f.path, err)
		}
		processed++
		metrics.SpoolReplayTotal.WithLabelValues("success").Inc()
	}
	return
}

// replayRecord deserializes a spool record and attempts to replay it.
// Returns nil on success (caller should delete the file), error on failure (caller keeps the file).
func (w *SpoolReplayWorker) replayRecord(ctx context.Context, data []byte) error {
	var rec localSpoolRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		// Malformed JSON — no point retrying, treat as permanent failure
		return fmt.Errorf("unmarshal: %w (permanent)", err)
	}

	// Step 1: Try to write to Redis offline queue
	redisOK := false
	if w.msgDAO != nil {
		if err := w.msgDAO.AddToOfflineQueue(ctx, rec.ToUID, rec.MsgID, float64(rec.Seq)); err != nil {
			log.Warningf("spool replay: redis offline queue failed for %s: %v", rec.MsgID, err)
		} else {
			redisOK = true
		}
	}

	// Step 2: Try to enqueue to Kafka
	kafkaOK := false
	if w.producer != nil {
		pushMsg := pushMsgBytes(pbPush, rec.Op, rec.Server, rec.Keys, "", rec.Body, 0)
		uidKey := fmt.Sprintf("%d", rec.ToUID)
		msg := &mq.Message{
			Key:     uidKey,
			Value:   pushMsg,
			Headers: rec.Headers,
		}
		if err := w.producer.EnqueueToUser(ctx, rec.ToUID, msg); err != nil {
			log.Warningf("spool replay: kafka enqueue failed for %s: %v", rec.MsgID, err)
		} else {
			kafkaOK = true
		}
	}

	// At least one must succeed
	if redisOK || kafkaOK {
		log.Infof("spool replay: replayed %s uid=%d redis=%v kafka=%v", rec.MsgID, rec.ToUID, redisOK, kafkaOK)
		return nil
	}
	return fmt.Errorf("both redis and kafka failed for msg %s", rec.MsgID)
}

// moveToExpired moves a file to the expired/ subdirectory.
func (w *SpoolReplayWorker) moveToExpired(dirPath, fileName string, data []byte) {
	expiredDir := filepath.Join(dirPath, "expired")
	if err := os.MkdirAll(expiredDir, 0o755); err != nil {
		log.Errorf("spool replay: mkdir expired dir: %v", err)
		return
	}
	src := filepath.Join(dirPath, fileName)
	dst := filepath.Join(expiredDir, fileName)
	if err := os.Rename(src, dst); err != nil {
		// Rename may fail across filesystems; fall back to copy+delete
		if writeErr := os.WriteFile(dst, data, 0o644); writeErr != nil {
			log.Errorf("spool replay: move to expired failed: %v", writeErr)
			return
		}
		os.Remove(src)
	}
	log.Infof("spool replay: expired %s (age > %s)", fileName, w.maxAge)
}

// updateFileCount scans all date directories and updates the SpoolFileCount gauge.
func (w *SpoolReplayWorker) updateFileCount() {
	entries, err := os.ReadDir(w.spoolDir)
	if err != nil {
		return
	}
	var count int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := time.Parse("20060102", entry.Name()); err != nil {
			continue
		}
		files, err := os.ReadDir(filepath.Join(w.spoolDir, entry.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".json") {
				count++
			}
		}
	}
	metrics.SpoolFileCount.Set(float64(count))
}

func defaultSpoolDir() string {
	if dir := os.Getenv("GOIM_UNDELIVERED_SPOOL_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "goim-undelivered")
}
