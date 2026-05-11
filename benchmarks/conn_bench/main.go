// conn_bench.go - TCP connection stress test for goim Comet server.
//
// Usage:
//
//	go run benchmarks/conn_bench.go -host=localhost:3101 -count=1000 -ramp=30s -duration=5m -output=conn_results.json
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	rawHeaderLen   = uint16(16)
	opHeartbeat    = int32(2)
	opHeartbeatRep = int32(3)
	opAuth         = int32(7)
	opAuthReply    = int32(8)
	heartbeatEvery = 240 * time.Second
)

var (
	host     string
	count    int
	ramp     time.Duration
	duration time.Duration
	output   string
)

type ConnResult struct {
	Mid         int64         `json:"mid"`
	ConnectTime time.Duration `json:"connect_ms"`
	AuthTime    time.Duration `json:"auth_ms"`
	AliveTime   time.Duration `json:"alive_ms"`
	Error       string        `json:"error,omitempty"`
	Timestamp   time.Time     `json:"timestamp"`
}

type ConnReport struct {
	StartTime    time.Time    `json:"start_time"`
	EndTime      time.Time    `json:"end_time"`
	TargetConns  int          `json:"target_conns"`
	ActualConns  int          `json:"actual_conns"`
	FailedConns  int          `json:"failed_conns"`
	AvgConnectMs float64      `json:"avg_connect_ms"`
	P50ConnectMs float64      `json:"p50_connect_ms"`
	P95ConnectMs float64      `json:"p95_connect_ms"`
	P99ConnectMs float64      `json:"p99_connect_ms"`
	ConnsPerSec  float64      `json:"conns_per_sec"`
	Results      []ConnResult `json:"results"`
}

func init() {
	flag.StringVar(&host, "host", "localhost:3101", "comet TCP address")
	flag.IntVar(&count, "count", 1000, "number of connections to create")
	flag.DurationVar(&ramp, "ramp", 30*time.Second, "ramp-up period")
	flag.DurationVar(&duration, "duration", 5*time.Second, "how long to keep connections alive")
	flag.StringVar(&output, "output", "conn_results.json", "output JSON file")
}

func main() {
	flag.Parse()
	fmt.Printf("=== Connection Benchmark ===\n")
	fmt.Printf("Target: %s, Count: %d, Ramp: %s, Duration: %s\n", host, count, ramp, duration)

	report := &ConnReport{
		StartTime:   time.Now(),
		TargetConns: count,
		Results:     make([]ConnResult, 0, count),
	}

	var (
		mu           sync.Mutex
		wg           sync.WaitGroup
		aliveCount   int64
		successCount int64
		failCount    int64
	)

	interval := ramp / time.Duration(count)
	if interval < time.Millisecond {
		interval = time.Millisecond
	}

	quit := make(chan struct{})
	done := make(chan struct{})

	// Progress reporter
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				fmt.Printf("  alive=%d success=%d fail=%d\n",
					atomic.LoadInt64(&aliveCount),
					atomic.LoadInt64(&successCount),
					atomic.LoadInt64(&failCount))
			case <-done:
				return
			}
		}
	}()

	// Spawn connections
	for i := 0; i < count; i++ {
		wg.Add(1)
		mid := int64(i + 1)
		go func() {
			defer wg.Done()
			result := connectClient(mid)
			mu.Lock()
			report.Results = append(report.Results, result)
			mu.Unlock()

			if result.Error != "" {
				atomic.AddInt64(&failCount, 1)
				return
			}
			atomic.AddInt64(&successCount, 1)
			atomic.AddInt64(&aliveCount, 1)
			defer atomic.AddInt64(&aliveCount, -1)

			// Keep alive until quit
			<-quit
		}()
		time.Sleep(interval)
	}

	// Wait for all connections to establish
	wg.Wait()
	close(done)

	// Keep alive for duration
	fmt.Printf("All connections established. Keeping alive for %s...\n", duration)
	time.Sleep(duration)

	close(quit)
	time.Sleep(time.Second) // let goroutines clean up

	report.EndTime = time.Now()
	report.ActualConns = int(atomic.LoadInt64(&successCount))
	report.FailedConns = int(atomic.LoadInt64(&failCount))

	// Calculate stats
	elapsed := report.EndTime.Sub(report.StartTime).Seconds()
	if elapsed > 0 {
		report.ConnsPerSec = float64(report.ActualConns) / elapsed
	}

	connectTimes := make([]float64, 0, report.ActualConns)
	for _, r := range report.Results {
		if r.Error == "" {
			connectTimes = append(connectTimes, float64(r.ConnectTime+r.AuthTime)/float64(time.Millisecond))
		}
	}
	if len(connectTimes) > 0 {
		sort.Float64s(connectTimes)
		report.AvgConnectMs = avg(connectTimes)
		report.P50ConnectMs = percentile(connectTimes, 50)
		report.P95ConnectMs = percentile(connectTimes, 95)
		report.P99ConnectMs = percentile(connectTimes, 99)
	}

	// Output
	data, _ := json.MarshalIndent(report, "", "  ")
	if output != "" {
		os.WriteFile(output, data, 0644)
		fmt.Printf("Report saved to %s\n", output)
	}
	fmt.Printf("\n=== Results ===\n")
	fmt.Printf("Connected: %d / %d (failed: %d)\n", report.ActualConns, report.TargetConns, report.FailedConns)
	fmt.Printf("Conns/sec: %.1f\n", report.ConnsPerSec)
	fmt.Printf("Connect+Auth latency: avg=%.2fms p50=%.2fms p95=%.2fms p99=%.2fms\n",
		report.AvgConnectMs, report.P50ConnectMs, report.P95ConnectMs, report.P99ConnectMs)
}

func connectClient(mid int64) ConnResult {
	result := ConnResult{Mid: mid, Timestamp: time.Now()}

	// TCP connect
	dialStart := time.Now()
	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.ConnectTime = time.Since(dialStart)

	defer conn.Close()

	// Auth
	authStart := time.Now()
	token := fmt.Sprintf(`{"mid":%d,"key":"bench-%d-%d","room_id":"test://1","platform":"bench","accepts":[9]}`,
		mid, mid, time.Now().UnixNano())
	if err := writeProto(conn, opAuth, 1, []byte(token)); err != nil {
		result.Error = "auth write: " + err.Error()
		return result
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := readAndVerify(conn, opAuthReply); err != nil {
		result.Error = "auth read: " + err.Error()
		return result
	}
	result.AuthTime = time.Since(authStart)
	conn.SetReadDeadline(time.Time{})

	// Heartbeat goroutine
	go func() {
		seq := int32(2)
		ticker := time.NewTicker(heartbeatEvery)
		defer ticker.Stop()
		for range ticker.C {
			if err := writeProto(conn, opHeartbeat, seq, nil); err != nil {
				return
			}
			seq++
		}
	}()

	// Read loop (just consume responses)
	go func() {
		for {
			conn.SetReadDeadline(time.Now().Add(heartbeatEvery + 60*time.Second))
			if err := readProto(conn); err != nil {
				return
			}
		}
	}()

	return result
}

func writeProto(conn net.Conn, op int32, seq int32, body []byte) error {
	hdr := make([]byte, rawHeaderLen)
	binary.BigEndian.PutUint32(hdr[0:], uint32(rawHeaderLen)+uint32(len(body)))
	binary.BigEndian.PutUint16(hdr[4:], rawHeaderLen)
	binary.BigEndian.PutUint16(hdr[6:], 1) // ver
	binary.BigEndian.PutUint32(hdr[8:], uint32(op))
	binary.BigEndian.PutUint32(hdr[12:], uint32(seq))
	if _, err := conn.Write(hdr); err != nil {
		return err
	}
	if len(body) > 0 {
		if _, err := conn.Write(body); err != nil {
			return err
		}
	}
	return nil
}

func readProto(conn net.Conn) error {
	hdr := make([]byte, rawHeaderLen)
	if _, err := readFull(conn, hdr); err != nil {
		return err
	}
	packLen := binary.BigEndian.Uint32(hdr[0:])
	headerLen := binary.BigEndian.Uint16(hdr[4:])
	bodyLen := int(packLen) - int(headerLen)
	if bodyLen > 0 {
		body := make([]byte, bodyLen)
		if _, err := readFull(conn, body); err != nil {
			return err
		}
	}
	return nil
}

func readAndVerify(conn net.Conn, expectedOp int32) error {
	hdr := make([]byte, rawHeaderLen)
	if _, err := readFull(conn, hdr); err != nil {
		return err
	}
	op := int32(binary.BigEndian.Uint32(hdr[8:]))
	packLen := binary.BigEndian.Uint32(hdr[0:])
	headerLen := binary.BigEndian.Uint16(hdr[4:])
	bodyLen := int(packLen) - int(headerLen)
	if bodyLen > 0 {
		body := make([]byte, bodyLen)
		if _, err := readFull(conn, body); err != nil {
			return err
		}
	}
	if op != expectedOp {
		return fmt.Errorf("expected op=%d, got op=%d", expectedOp, op)
	}
	return nil
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		nn, err := conn.Read(buf[n:])
		if err != nil {
			return n, err
		}
		n += nn
	}
	return n, nil
}

func avg(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range data {
		sum += v
	}
	return sum / float64(len(data))
}

func percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := float64(p) / 100.0 * float64(len(sorted)-1)
	lo := int(idx)
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
