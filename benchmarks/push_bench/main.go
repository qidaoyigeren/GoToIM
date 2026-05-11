// push_bench.go - Push throughput and end-to-end latency benchmark for goim.
//
// Usage:
//
//	go run benchmarks/push_bench.go -logic-host=localhost:3111 -comet-host=localhost:3102 -receivers=100 -rate=1000 -duration=60s -output=push_results.json
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	rawHeaderLen = 16
	opHeartbeat  = int32(2)
	opAuth       = int32(7)
	opAuthReply  = int32(8)
	opRaw        = int32(9)
)

var (
	logicHost  string
	cometHost  string
	receivers  int
	rate       int
	benchDur   time.Duration
	outputFile string
)

type PushResult struct {
	SenderMid   int64   `json:"sender_mid"`
	ReceiverMid int64   `json:"receiver_mid"`
	SendTime    int64   `json:"send_ts"`
	RecvTime    int64   `json:"recv_ts"`
	LatencyMs   float64 `json:"latency_ms"`
}

type PushReport struct {
	StartTime     time.Time    `json:"start_time"`
	EndTime       time.Time    `json:"end_time"`
	TotalSent     int64        `json:"total_sent"`
	TotalReceived int64        `json:"total_received"`
	FailedSent    int64        `json:"failed_sent"`
	MsgsPerSec    float64      `json:"msgs_per_sec"`
	AvgLatencyMs  float64      `json:"avg_latency_ms"`
	P50LatencyMs  float64      `json:"p50_latency_ms"`
	P95LatencyMs  float64      `json:"p95_latency_ms"`
	P99LatencyMs  float64      `json:"p99_latency_ms"`
	MaxLatencyMs  float64      `json:"max_latency_ms"`
	Results       []PushResult `json:"results"`
}

func init() {
	flag.StringVar(&logicHost, "logic-host", "localhost:3111", "logic HTTP address")
	flag.StringVar(&cometHost, "comet-host", "localhost:3102", "comet WebSocket address")
	flag.IntVar(&receivers, "receivers", 100, "number of receiver connections")
	flag.IntVar(&rate, "rate", 1000, "target messages per second")
	flag.DurationVar(&benchDur, "duration", 60*time.Second, "benchmark duration")
	flag.StringVar(&outputFile, "output", "push_results.json", "output JSON file")
}

func main() {
	flag.Parse()
	fmt.Printf("=== Push Benchmark ===\n")
	fmt.Printf("Logic: %s, Comet: %s\n", logicHost, cometHost)
	fmt.Printf("Receivers: %d, Rate: %d msg/s, Duration: %s\n", receivers, rate, benchDur)

	report := &PushReport{
		StartTime: time.Now(),
		Results:   make([]PushResult, 0, rate*int(benchDur.Seconds())),
	}

	var (
		mu           sync.Mutex
		totalSent    int64
		totalRecv    int64
		failedSent   int64
		receiverMids = make([]int64, receivers)
	)

	// Phase 1: Establish receiver connections (TCP, simpler than WS for benchmarks)
	fmt.Printf("\n--- Phase 1: Establishing %d receivers ---\n", receivers)
	conns := make([]net.Conn, 0, receivers)
	for i := 0; i < receivers; i++ {
		mid := int64(10000 + i)
		receiverMids[i] = mid
		conn, err := connectReceiver(mid)
		if err != nil {
			fmt.Printf("  Failed to connect receiver mid=%d: %v\n", mid, err)
			continue
		}
		conns = append(conns, conn)
		if (i+1)%50 == 0 {
			fmt.Printf("  Connected %d/%d receivers\n", i+1, receivers)
		}
	}
	fmt.Printf("  %d receivers connected\n", len(conns))

	if len(conns) == 0 {
		fmt.Println("No receivers connected, aborting.")
		return
	}

	// Read pump for each receiver
	quit := make(chan struct{})
	for i, conn := range conns {
		mid := receiverMids[i]
		go readPump(conn, mid, &mu, report, &totalRecv, quit)
	}

	// Phase 2: Send messages
	fmt.Printf("\n--- Phase 2: Sending messages at %d msg/s for %s ---\n", rate, benchDur)
	httpClient := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
		},
		Timeout: 10 * time.Second,
	}

	ticker := time.NewTicker(time.Second / time.Duration(rate))
	defer ticker.Stop()
	timer := time.NewTimer(benchDur)
	defer timer.Stop()

	senderMid := int64(9999)
	receiverIdx := 0

sendLoop:
	for {
		select {
		case <-timer.C:
			break sendLoop
		case <-ticker.C:
			targetMid := receiverMids[receiverIdx%len(receiverMids)]
			receiverIdx++
			ts := time.Now().UnixMilli()
			payload := fmt.Sprintf(`{"ts":%d,"from":%d,"msg":"bench"}`, ts, senderMid)

			go func(mid int64, body string, sendTs int64) {
				url := fmt.Sprintf("http://%s/goim/push/mids?operation=9&mids=%d", logicHost, mid)
				resp, err := httpClient.Post(url, "application/json", bytes.NewBufferString(body))
				if err != nil {
					atomic.AddInt64(&failedSent, 1)
					return
				}
				resp.Body.Close()
				atomic.AddInt64(&totalSent, 1)
			}(targetMid, payload, ts)
		}
	}

	// Wait for remaining messages to arrive
	time.Sleep(5 * time.Second)
	close(quit)
	time.Sleep(time.Second)

	report.EndTime = time.Now()
	report.TotalSent = atomic.LoadInt64(&totalSent)
	report.TotalReceived = atomic.LoadInt64(&totalRecv)
	report.FailedSent = atomic.LoadInt64(&failedSent)

	// Calculate stats
	elapsed := report.EndTime.Sub(report.StartTime).Seconds()
	if elapsed > 0 {
		report.MsgsPerSec = float64(report.TotalSent) / elapsed
	}

	latencies := make([]float64, 0, len(report.Results))
	for _, r := range report.Results {
		latencies = append(latencies, r.LatencyMs)
	}
	if len(latencies) > 0 {
		sort.Float64s(latencies)
		report.AvgLatencyMs = avg(latencies)
		report.P50LatencyMs = percentile(latencies, 50)
		report.P95LatencyMs = percentile(latencies, 95)
		report.P99LatencyMs = percentile(latencies, 99)
		report.MaxLatencyMs = latencies[len(latencies)-1]
	}

	// Output
	data, _ := json.MarshalIndent(report, "", "  ")
	if outputFile != "" {
		os.WriteFile(outputFile, data, 0644)
		fmt.Printf("Report saved to %s\n", outputFile)
	}

	fmt.Printf("\n=== Results ===\n")
	fmt.Printf("Sent: %d, Received: %d, Failed: %d\n", report.TotalSent, report.TotalReceived, report.FailedSent)
	fmt.Printf("QPS: %.1f\n", report.MsgsPerSec)
	fmt.Printf("E2E Latency: avg=%.2fms p50=%.2fms p95=%.2fms p99=%.2fms max=%.2fms\n",
		report.AvgLatencyMs, report.P50LatencyMs, report.P95LatencyMs, report.P99LatencyMs, report.MaxLatencyMs)

	// Close all connections
	for _, conn := range conns {
		conn.Close()
	}
}

func connectReceiver(mid int64) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", cometHost, 5*time.Second)
	if err != nil {
		return nil, err
	}

	// Auth
	token := fmt.Sprintf(`{"mid":%d,"key":"bench-recv-%d","room_id":"test://1","platform":"bench","accepts":[9]}`, mid, mid)
	if err := writeProto(conn, opAuth, 1, []byte(token)); err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := readExpectOp(conn, opAuthReply); err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetReadDeadline(time.Time{})

	// Heartbeat goroutine
	go func() {
		seq := int32(2)
		ticker := time.NewTicker(240 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := writeProto(conn, opHeartbeat, seq, nil); err != nil {
				return
			}
			seq++
		}
	}()

	return conn, nil
}

func readPump(conn net.Conn, mid int64, mu *sync.Mutex, report *PushReport, totalRecv *int64, quit chan struct{}) {
	for {
		select {
		case <-quit:
			return
		default:
		}
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		op, body, err := readProtoWithBody(conn)
		if err != nil {
			return
		}
		switch op {
		case opRaw:
			// Parse latency from body
			var msg struct {
				Ts   int64 `json:"ts"`
				From int64 `json:"from"`
			}
			if json.Unmarshal(body, &msg) == nil && msg.Ts > 0 {
				recvTs := time.Now().UnixMilli()
				latency := float64(recvTs-msg.Ts) / float64(time.Millisecond)
				atomic.AddInt64(totalRecv, 1)
				mu.Lock()
				report.Results = append(report.Results, PushResult{
					SenderMid:   msg.From,
					ReceiverMid: mid,
					SendTime:    msg.Ts,
					RecvTime:    recvTs,
					LatencyMs:   latency,
				})
				mu.Unlock()
			}
		}
	}
}

func writeProto(conn net.Conn, op int32, seq int32, body []byte) error {
	hdr := make([]byte, rawHeaderLen)
	binary.BigEndian.PutUint32(hdr[0:], uint32(rawHeaderLen)+uint32(len(body)))
	binary.BigEndian.PutUint16(hdr[4:], rawHeaderLen)
	binary.BigEndian.PutUint16(hdr[6:], 1)
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

func readProtoWithBody(conn net.Conn) (int32, []byte, error) {
	hdr := make([]byte, rawHeaderLen)
	if _, err := readFull(conn, hdr); err != nil {
		return 0, nil, err
	}
	packLen := binary.BigEndian.Uint32(hdr[0:])
	headerLen := binary.BigEndian.Uint16(hdr[4:])
	op := int32(binary.BigEndian.Uint32(hdr[8:]))
	bodyLen := int(packLen) - int(headerLen)
	var body []byte
	if bodyLen > 0 {
		body = make([]byte, bodyLen)
		if _, err := readFull(conn, body); err != nil {
			return 0, nil, err
		}
	}
	return op, body, nil
}

func readExpectOp(conn net.Conn, expectedOp int32) error {
	op, _, err := readProtoWithBody(conn)
	if err != nil {
		return err
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
	lo := int(math.Floor(idx))
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
