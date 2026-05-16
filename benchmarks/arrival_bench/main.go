// arrival_bench.go - Message arrival-rate reliability benchmark for goim.
//
// Usage:
//
//	go run benchmarks/arrival_bench/main.go -receivers=5000 -kill=0.3 -msgs=1000
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
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
	rawHeaderLen   = 16
	opHeartbeat    = int32(2)
	opAuth         = int32(7)
	opAuthReply    = int32(8)
	opRaw          = int32(9)
	heartbeatEvery = 240 * time.Second
)

var (
	logicHost  string
	cometHost  string
	receivers  int
	killRatio  float64
	msgsPerRcv int
	outputFile string
)

func init() {
	flag.StringVar(&logicHost, "logic-host", "localhost:3111", "logic HTTP address")
	flag.StringVar(&cometHost, "comet-host", "localhost:3101", "comet TCP address")
	flag.IntVar(&receivers, "receivers", 5000, "total number of receiver connections")
	flag.Float64Var(&killRatio, "kill", 0.3, "fraction of receivers to kill (0.0-1.0)")
	flag.IntVar(&msgsPerRcv, "msgs", 1000, "messages sent per receiver per phase")
	flag.StringVar(&outputFile, "output", "arrival_results.json", "output JSON file")
}

type ArrivalReport struct {
	StartTime        time.Time `json:"start_time"`
	EndTime          time.Time `json:"end_time"`
	TotalReceivers   int       `json:"total_receivers"`
	KilledReceivers  int       `json:"killed_receivers"`
	OnlineSent       int64     `json:"online_sent"`
	OnlineReceived   int64     `json:"online_received"`
	OnlineRate       float64   `json:"online_arrival_rate"`
	OfflineSent      int64     `json:"offline_sent"`
	OfflineRecovered int64     `json:"offline_recovered"`
	OfflineRate      float64   `json:"offline_arrival_rate"`
	TotalSent        int64     `json:"total_sent"`
	TotalReceived    int64     `json:"total_received"`
	OverallRate      float64   `json:"overall_arrival_rate"`
	AvgLatencyMs     float64   `json:"avg_latency_ms"`
	P50LatencyMs     float64   `json:"p50_latency_ms"`
	P99LatencyMs     float64   `json:"p99_latency_ms"`
	MaxLatencyMs     float64   `json:"max_latency_ms"`
	Latencies        []float64 `json:"latencies,omitempty"`
}

type receiver struct {
	mid      int64
	conn     net.Conn
	stable   bool
	received map[string]int64
	mu       sync.Mutex
}

func main() {
	flag.Parse()

	stableCount := receivers - int(float64(receivers)*killRatio)
	if stableCount < 1 {
		stableCount = 1
	}
	killedCount := receivers - stableCount

	fmt.Printf("=== Arrival Rate Benchmark ===\n")
	fmt.Printf("Logic: %s, Comet: %s\n", logicHost, cometHost)
	fmt.Printf("Receivers: %d total (%d stable, %d will be killed)\n", receivers, stableCount, killedCount)
	fmt.Printf("Messages per receiver per phase: %d\n", msgsPerRcv)
	fmt.Println()

	report := &ArrivalReport{
		StartTime:       time.Now(),
		TotalReceivers:  receivers,
		KilledReceivers: killedCount,
	}

	var (
		allReceivers []*receiver
		stableRcvs   []*receiver
		killedRcvs   []*receiver
	)

	fmt.Printf("── Phase 1: Connecting %d receivers ──\n", receivers)
	for i := 0; i < receivers; i++ {
		mid := int64(20000 + i)
		stable := i < stableCount
		conn, err := connectReceiver(mid)
		if err != nil {
			fmt.Printf("  FAILED to connect mid=%d: %v\n", mid, err)
			continue
		}
		r := &receiver{mid: mid, conn: conn, stable: stable, received: make(map[string]int64)}
		allReceivers = append(allReceivers, r)
		if stable {
			stableRcvs = append(stableRcvs, r)
		} else {
			killedRcvs = append(killedRcvs, r)
		}
		if (i+1)%50 == 0 {
			fmt.Printf("  Connected %d/%d\n", i+1, receivers)
		}
	}
	fmt.Printf("  Total connected: %d (stable=%d, unstable=%d)\n\n", len(allReceivers), len(stableRcvs), len(killedRcvs))

	if len(allReceivers) == 0 {
		fmt.Println("No receivers connected, aborting.")
		return
	}

	quit := make(chan struct{})
	var phase1Latencies []float64
	var latMu sync.Mutex

	for _, r := range allReceivers {
		go readPumpTracking(r, quit, &phase1Latencies, &latMu)
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        200,
			MaxIdleConnsPerHost: 200,
		},
		Timeout: 10 * time.Second,
	}

	// Phase 2: Send messages while all online (rate-limited like push_bench)
	fmt.Printf("── Phase 2: Sending %d msgs to each of %d online receivers ──\n", msgsPerRcv, len(allReceivers))
	totalPhase1 := int64(len(allReceivers)) * int64(msgsPerRcv)
	phase1Sent := sendMessagesRateLimited(allReceivers, msgsPerRcv, "online-phase", httpClient, totalPhase1)
	report.OnlineSent = phase1Sent
	fmt.Printf("  Phase 2 complete: %d messages sent\n\n", phase1Sent)

	time.Sleep(3 * time.Second)

	var phase1Recv int64
	for _, r := range allReceivers {
		r.mu.Lock()
		phase1Recv += int64(len(r.received))
		r.mu.Unlock()
	}
	report.OnlineReceived = phase1Recv
	if phase1Sent > 0 {
		report.OnlineRate = float64(phase1Recv) / float64(phase1Sent) * 100
	}
	fmt.Printf("  Phase 2 received: %d / %d (%.2f%%)\n\n", phase1Recv, phase1Sent, report.OnlineRate)

	fmt.Printf("── Phase 3: Killing %d receivers ──\n", len(killedRcvs))
	for _, r := range killedRcvs {
		r.conn.Close()
	}
	for _, r := range killedRcvs {
		r.mu.Lock()
		r.received = make(map[string]int64)
		r.mu.Unlock()
	}

	fmt.Println("  Waiting 10s for offline propagation...")
	time.Sleep(10 * time.Second)
	fmt.Println()

	fmt.Printf("── Phase 4: Sending %d msgs to each of %d OFFLINE receivers ──\n", msgsPerRcv, len(killedRcvs))
	if len(killedRcvs) > 0 {
		totalPhase4 := int64(len(killedRcvs)) * int64(msgsPerRcv)
		phase4Sent := sendMessagesRateLimited(killedRcvs, msgsPerRcv, "offline-phase", httpClient, totalPhase4)
		report.OfflineSent = phase4Sent
		fmt.Printf("  Phase 4 complete: %d messages sent\n\n", phase4Sent)
		time.Sleep(3 * time.Second)
	}

	fmt.Printf("── Phase 5: Reconnecting %d receivers ──\n", len(killedRcvs))
	reconnected := 0
	for _, r := range killedRcvs {
		conn, err := connectReceiver(r.mid)
		if err != nil {
			fmt.Printf("  FAILED to reconnect mid=%d: %v\n", r.mid, err)
			continue
		}
		r.conn = conn
		reconnected++
	}
	fmt.Printf("  Reconnected: %d / %d\n\n", reconnected, len(killedRcvs))

	fmt.Printf("── Phase 6: Pulling offline messages via sync API ──\n")
	var offlineRecovered int64
	for _, r := range killedRcvs {
		if r.conn == nil {
			continue
		}
		count := pullOfflineMessages(httpClient, r.mid)
		offlineRecovered += count
	}
	report.OfflineRecovered = offlineRecovered
	if report.OfflineSent > 0 {
		report.OfflineRate = float64(offlineRecovered) / float64(report.OfflineSent) * 100
	}
	fmt.Printf("  Recovered from offline queue: %d / %d (%.2f%%)\n\n", offlineRecovered, report.OfflineSent, report.OfflineRate)

	report.TotalSent = report.OnlineSent + report.OfflineSent
	report.TotalReceived = phase1Recv + offlineRecovered
	if report.TotalSent > 0 {
		report.OverallRate = float64(report.TotalReceived) / float64(report.TotalSent) * 100
	}

	sort.Float64s(phase1Latencies)
	if len(phase1Latencies) > 0 {
		report.Latencies = phase1Latencies
		report.AvgLatencyMs = avg(phase1Latencies)
		report.P50LatencyMs = percentile(phase1Latencies, 50)
		report.P99LatencyMs = percentile(phase1Latencies, 99)
		report.MaxLatencyMs = phase1Latencies[len(phase1Latencies)-1]
	}

	report.EndTime = time.Now()
	close(quit)
	time.Sleep(500 * time.Millisecond)
	for _, r := range allReceivers {
		if r.conn != nil {
			r.conn.Close()
		}
	}

	data, _ := json.MarshalIndent(report, "", "  ")
	if outputFile != "" {
		os.WriteFile(outputFile, data, 0644)
		fmt.Printf("Report saved to %s\n", outputFile)
	}

	fmt.Printf("=== Results ===\n")
	fmt.Printf("Online arrival:  %.2f%% (%d/%d)\n", report.OnlineRate, report.OnlineReceived, report.OnlineSent)
	fmt.Printf("Offline arrival: %.2f%% (%d/%d)\n", report.OfflineRate, report.OfflineRecovered, report.OfflineSent)
	fmt.Printf("Overall arrival: %.2f%% (%d/%d)\n", report.OverallRate, report.TotalReceived, report.TotalSent)
	if len(phase1Latencies) > 0 {
		fmt.Printf("E2E Latency (online): avg=%.2fms p50=%.2fms p99=%.2fms max=%.2fms\n",
			report.AvgLatencyMs, report.P50LatencyMs, report.P99LatencyMs, report.MaxLatencyMs)
	}
}

func connectReceiver(mid int64) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", cometHost, 5*time.Second)
	if err != nil {
		return nil, err
	}

	token := fmt.Sprintf(`{"mid":%d,"key":"arrival-%d-%d","room_id":"test://1","platform":"bench","accepts":[9]}`,
		mid, mid, time.Now().UnixNano())
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

	return conn, nil
}

func readPumpTracking(r *receiver, quit chan struct{}, latencies *[]float64, latMu *sync.Mutex) {
	for {
		select {
		case <-quit:
			return
		default:
		}
		r.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		op, body, err := readProtoWithBody(r.conn)
		if err != nil {
			return
		}
		if op == opRaw {
			content := extractMsgBodyContent(body)
			var msg struct {
				Ts   int64 `json:"ts"`
				From int64 `json:"from"`
			}
			if json.Unmarshal(content, &msg) == nil && msg.Ts > 0 {
				recvTs := time.Now().UnixMilli()
				latency := float64(recvTs - msg.Ts)
				r.mu.Lock()
				r.received[fmt.Sprintf("%d", msg.Ts)] = recvTs
				r.mu.Unlock()
				latMu.Lock()
				*latencies = append(*latencies, latency)
				latMu.Unlock()
			}
		}
	}
}

func sendMessagesRateLimited(rcvs []*receiver, nPerRcv int, label string, httpClient *http.Client, totalMsgs int64) int64 {
	rate := 500 // msgs/sec — fast enough but won't overwhelm Logic
	ticker := time.NewTicker(time.Second / time.Duration(rate))
	defer ticker.Stop()

	var (
		sent  int64
		fails int64
		idx   int
	)
	target := int(totalMsgs)

	for i := 0; i < target; i++ {
		<-ticker.C
		rcv := rcvs[idx%len(rcvs)]
		idx++

		go func(r *receiver, seqNum int) {
			ts := time.Now().UnixMilli()
			payload := fmt.Sprintf(`{"ts":%d,"from":19999,"msg":"%s-%d"}`, ts, label, seqNum)
			url := fmt.Sprintf("http://%s/goim/push/mids?operation=9&mids=%d", logicHost, r.mid)
			resp, err := httpClient.Post(url, "application/json", bytes.NewBufferString(payload))
			if err != nil {
				atomic.AddInt64(&fails, 1)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			atomic.AddInt64(&sent, 1)
		}(rcv, i)
	}

	// Wait for in-flight requests to complete
	time.Sleep(2 * time.Second)
	return atomic.LoadInt64(&sent)
}

func pullOfflineMessages(httpClient *http.Client, mid int64) int64 {
	url := fmt.Sprintf("http://%s/goim/sync?mid=%d&last_seq=-1&limit=200", logicHost, mid)
	resp, err := httpClient.Get(url)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0
	}

	if len(body) < 13 {
		return 0
	}
	return int64(binary.BigEndian.Uint32(body[9:13]))
}

func extractMsgBodyContent(body []byte) []byte {
	if len(body) < 2 {
		return body
	}
	msgIDLen := int(binary.BigEndian.Uint16(body[0:2]))
	offset := 2 + msgIDLen + 8 + 8 + 8 + 8
	if offset < len(body) {
		return body[offset:]
	}
	return body
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
