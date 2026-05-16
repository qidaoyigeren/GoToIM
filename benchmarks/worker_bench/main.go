// worker_bench.go - DeliveryWorker throughput benchmark for goim.
//
// Produces pb.PushMsg messages directly to Kafka; the Worker consumes and
// dispatches them via gRPC to Comet, where connected receivers count them.
//
// Usage:
//
//	go run benchmarks/worker_bench/main.go -receivers=1000 -msgs=500000
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/IBM/sarama"
	pb "github.com/Terry-Mao/goim/api/logic"
	"google.golang.org/protobuf/proto"
)

const (
	goimHeaderLen  = 16
	goimOpAuth     = int32(7)
	goimOpAuthRply = int32(8)
	goimOpRaw      = int32(9)
	goimHBeat      = int32(2)
	goimHBeatEvery = 240 * time.Second
)

var (
	cometHost    string
	kafkaBroker  string
	kafkaTopic   string
	serverID     string
	receiverN    int
	totalMsgs    int
	workerOutput string
	batchSize    int
)

func init() {
	flag.StringVar(&cometHost, "comet-host", "localhost:3101", "comet TCP address")
	flag.StringVar(&kafkaBroker, "kafka-brokers", "localhost:9092", "Kafka bootstrap broker")
	flag.StringVar(&kafkaTopic, "topic", "goim-push-topic", "Kafka push topic name")
	flag.StringVar(&serverID, "server-id", "comet", "Comet server name as registered in Discovery metadata")
	flag.IntVar(&receiverN, "receivers", 1000, "number of receiver connections")
	flag.IntVar(&totalMsgs, "msgs", 500000, "total messages to produce")
	flag.IntVar(&batchSize, "batch", 2000, "Kafka producer batch size per flush")
	flag.StringVar(&workerOutput, "output", "worker_results.json", "output JSON file")
}

type WorkerReport struct {
	StartTime      time.Time `json:"start_time"`
	EndTime        time.Time `json:"end_time"`
	Receivers      int       `json:"receivers"`
	TotalProduced  int64     `json:"total_produced"`
	TotalReceived  int64     `json:"total_received"`
	ProduceElapsed float64   `json:"produce_elapsed_sec"`
	ConsumeElapsed float64   `json:"consume_elapsed_sec"`
	ProduceRate    float64   `json:"produce_rate_msg_per_sec"`
	WorkerRate     float64   `json:"worker_rate_msg_per_sec"`
	AvgLatencyMs   float64   `json:"avg_latency_ms"`
	P50LatencyMs   float64   `json:"p50_latency_ms"`
	P95LatencyMs   float64   `json:"p95_latency_ms"`
	P99LatencyMs   float64   `json:"p99_latency_ms"`
	MaxLatencyMs   float64   `json:"max_latency_ms"`
}

type workerReceiver struct {
	mid      int64
	key      string
	conn     net.Conn
	received int64
}

func main() {
	flag.Parse()
	fmt.Printf("=== Worker Throughput Benchmark ===\n")
	fmt.Printf("Comet: %s, Kafka: %s, Topic: %s\n", cometHost, kafkaBroker, kafkaTopic)
	fmt.Printf("Server ID: %s, Receivers: %d, Messages: %d\n", serverID, receiverN, totalMsgs)
	fmt.Println()

	report := &WorkerReport{
		StartTime: time.Now(),
		Receivers: receiverN,
	}

	fmt.Printf("── Phase 1: Connecting %d receivers ──\n", receiverN)
	var receivers []*workerReceiver
	for i := 0; i < receiverN; i++ {
		mid := int64(30000 + i)
		key, conn, err := connectAndAuth(mid)
		if err != nil {
			fmt.Printf("  FAILED mid=%d: %v\n", mid, err)
			continue
		}
		r := &workerReceiver{mid: mid, key: key, conn: conn}
		receivers = append(receivers, r)
		if (i+1)%50 == 0 {
			fmt.Printf("  Connected %d/%d\n", i+1, receiverN)
		}
	}
	fmt.Printf("  Total connected: %d\n\n", len(receivers))

	if len(receivers) == 0 {
		fmt.Println("No receivers connected, aborting.")
		return
	}

	quit := make(chan struct{})
	var (
		latencies []float64
		latMu     sync.Mutex
	)

	for _, r := range receivers {
		go workerReadPump(r, quit, &latencies, &latMu)
	}

	fmt.Println("── Phase 2: Waiting for Comet online report (10s) ──")
	time.Sleep(10 * time.Second)
	fmt.Println()

	fmt.Printf("── Phase 3: Producing %d messages to Kafka ──\n", totalMsgs)
	produceStart := time.Now()

	producer, err := newKafkaAsyncProducer()
	if err != nil {
		fmt.Printf("  FAILED to create Kafka producer: %v\n", err)
		return
	}
	defer producer.Close()

	var produced int64
	var produceErr int64
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-producer.Successes():
				atomic.AddInt64(&produced, 1)
			case <-producer.Errors():
				atomic.AddInt64(&produceErr, 1)
			case <-done:
				return
			}
		}
	}()

	msgPerReceiver := totalMsgs / len(receivers)
	remainder := totalMsgs % len(receivers)
	var enqueued int64

	for i, r := range receivers {
		count := msgPerReceiver
		if i < remainder {
			count++
		}
		for j := 0; j < count; j++ {
			ts := time.Now().UnixMilli()
			body := fmt.Sprintf(`{"ts":%d,"mid":%d,"worker":"bench","idx":%d}`, ts, r.mid, j)

			pushMsg := &pb.PushMsg{
				Type:      pb.PushMsg_PUSH,
				Operation: goimOpRaw,
				Server:    serverID,
				Keys:      []string{r.key},
				Msg:       []byte(body),
			}

			b, err := proto.Marshal(pushMsg)
			if err != nil {
				atomic.AddInt64(&produceErr, 1)
				continue
			}

			km := &sarama.ProducerMessage{
				Topic: kafkaTopic,
				Key:   sarama.StringEncoder(fmt.Sprintf("%d", r.mid)),
				Value: sarama.ByteEncoder(b),
			}

			producer.Input() <- km
			enqueued++

			if enqueued%50000 == 0 {
				fmt.Printf("  Enqueued %d/%d (ack'd=%d err=%d)\n",
					enqueued, totalMsgs, atomic.LoadInt64(&produced), atomic.LoadInt64(&produceErr))
			}
		}
	}

	fmt.Println("  Waiting for Kafka producer to flush...")
	for {
		success := atomic.LoadInt64(&produced)
		errs := atomic.LoadInt64(&produceErr)
		if success+errs >= int64(totalMsgs) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	close(done)

	produceElapsed := time.Since(produceStart).Seconds()
	finalProduced := atomic.LoadInt64(&produced)
	finalProduceErr := atomic.LoadInt64(&produceErr)
	report.TotalProduced = finalProduced
	report.ProduceElapsed = produceElapsed
	if produceElapsed > 0 {
		report.ProduceRate = float64(finalProduced) / produceElapsed
	}
	fmt.Printf("  Produced: %d msgs in %.1fs (%.0f msg/s)\n", finalProduced, produceElapsed, report.ProduceRate)
	if finalProduceErr > 0 {
		fmt.Printf("  Produce errors: %d\n", finalProduceErr)
	}
	fmt.Println()

	fmt.Println("── Phase 4: Waiting for Worker to dispatch (15s) ──")
	consumeStart := time.Now()
	time.Sleep(15 * time.Second)

	var totalRecv int64
	for _, r := range receivers {
		totalRecv += atomic.LoadInt64(&r.received)
	}

	retries := 0
	for totalRecv < finalProduced && retries < 3 {
		time.Sleep(3 * time.Second)
		totalRecv = 0
		for _, r := range receivers {
			totalRecv += atomic.LoadInt64(&r.received)
		}
		retries++
	}

	consumeElapsed := time.Since(consumeStart).Seconds()
	report.TotalReceived = totalRecv
	report.ConsumeElapsed = consumeElapsed
	if consumeElapsed > 0 && totalRecv > 0 {
		report.WorkerRate = float64(totalRecv) / consumeElapsed
	}

	close(quit)
	time.Sleep(500 * time.Millisecond)

	sort.Float64s(latencies)
	if len(latencies) > 0 {
		report.AvgLatencyMs = avgFloat(latencies)
		report.P50LatencyMs = percentileFloat(latencies, 50)
		report.P95LatencyMs = percentileFloat(latencies, 95)
		report.P99LatencyMs = percentileFloat(latencies, 99)
		report.MaxLatencyMs = latencies[len(latencies)-1]
	}

	report.EndTime = time.Now()

	for _, r := range receivers {
		if r.conn != nil {
			r.conn.Close()
		}
	}

	data, _ := json.MarshalIndent(report, "", "  ")
	if workerOutput != "" {
		os.WriteFile(workerOutput, data, 0644)
		fmt.Printf("Report saved to %s\n", workerOutput)
	}

	fmt.Println()
	fmt.Printf("=== Results ===\n")
	fmt.Printf("Receivers: %d\n", len(receivers))
	fmt.Printf("Produced:  %d (%.0f msg/s)\n", report.TotalProduced, report.ProduceRate)
	fmt.Printf("Received:  %d / %d (%.1f%%)\n", report.TotalReceived, report.TotalProduced,
		float64(report.TotalReceived)/float64(report.TotalProduced)*100)
	fmt.Printf("Worker dispatch rate: ~%.0f msg/s\n", report.WorkerRate)
	if len(latencies) > 0 {
		fmt.Printf("E2E Latency: avg=%.2fms p50=%.2fms p95=%.2fms p99=%.2fms max=%.2fms\n",
			report.AvgLatencyMs, report.P50LatencyMs, report.P95LatencyMs, report.P99LatencyMs, report.MaxLatencyMs)
	}
}

func connectAndAuth(mid int64) (string, net.Conn, error) {
	conn, err := net.DialTimeout("tcp", cometHost, 5*time.Second)
	if err != nil {
		return "", nil, err
	}

	// Use the same key in the auth token that we'll later use for Kafka messages.
	// Logic.Connect preserves the client-provided key if non-empty.
	key := fmt.Sprintf("worker-%d-%d", mid, time.Now().UnixNano())

	token := fmt.Sprintf(`{"mid":%d,"key":"%s","room_id":"test://1","platform":"bench","accepts":[9]}`,
		mid, key)

	if err := writeTCPFrame(conn, goimOpAuth, 1, []byte(token)); err != nil {
		conn.Close()
		return "", nil, fmt.Errorf("auth write: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	op, _, err := readTCPFrame(conn)
	if err != nil {
		conn.Close()
		return "", nil, fmt.Errorf("auth read: %w", err)
	}
	if op != goimOpAuthRply {
		conn.Close()
		return "", nil, fmt.Errorf("expected op=%d, got %d", goimOpAuthRply, op)
	}
	conn.SetReadDeadline(time.Time{})

	go func() {
		seq := int32(2)
		ticker := time.NewTicker(goimHBeatEvery)
		defer ticker.Stop()
		for range ticker.C {
			if err := writeTCPFrame(conn, goimHBeat, seq, nil); err != nil {
				return
			}
			seq++
		}
	}()

	return key, conn, nil
}

func workerReadPump(r *workerReceiver, quit chan struct{}, latencies *[]float64, latMu *sync.Mutex) {
	for {
		select {
		case <-quit:
			return
		default:
		}
		r.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		op, body, err := readTCPFrame(r.conn)
		if err != nil {
			return
		}
		if op == goimOpRaw {
			// Worker path: raw JSON in body (no MsgBody wrapping unlike HTTP push)
			var msg struct {
				Ts  int64 `json:"ts"`
				Mid int64 `json:"mid"`
				Idx int   `json:"idx"`
			}
			if json.Unmarshal(body, &msg) == nil && msg.Ts > 0 {
				atomic.AddInt64(&r.received, 1)
				recvTs := time.Now().UnixMilli()
				latency := float64(recvTs - msg.Ts)
				latMu.Lock()
				*latencies = append(*latencies, latency)
				latMu.Unlock()
			}
		}
	}
}

func newKafkaAsyncProducer() (sarama.AsyncProducer, error) {
	cfg := sarama.NewConfig()
	cfg.Producer.RequiredAcks = sarama.WaitForLocal
	cfg.Producer.Retry.Max = 3
	cfg.Producer.Return.Successes = true
	cfg.Producer.Return.Errors = true
	cfg.Producer.Compression = sarama.CompressionSnappy
	cfg.Producer.Flush.Messages = batchSize
	cfg.Producer.Flush.Frequency = 50 * time.Millisecond
	cfg.Producer.MaxMessageBytes = 1048576
	return sarama.NewAsyncProducer([]string{kafkaBroker}, cfg)
}

func writeTCPFrame(conn net.Conn, op int32, seq int32, body []byte) error {
	hdr := make([]byte, goimHeaderLen)
	binary.BigEndian.PutUint32(hdr[0:], uint32(goimHeaderLen)+uint32(len(body)))
	binary.BigEndian.PutUint16(hdr[4:], goimHeaderLen)
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

func readTCPFrame(conn net.Conn) (int32, []byte, error) {
	hdr := make([]byte, goimHeaderLen)
	if _, err := readFullTCP(conn, hdr); err != nil {
		return 0, nil, err
	}
	packLen := binary.BigEndian.Uint32(hdr[0:])
	headerLen := binary.BigEndian.Uint16(hdr[4:])
	op := int32(binary.BigEndian.Uint32(hdr[8:]))
	bodyLen := int(packLen) - int(headerLen)
	var body []byte
	if bodyLen > 0 {
		body = make([]byte, bodyLen)
		if _, err := readFullTCP(conn, body); err != nil {
			return 0, nil, err
		}
	}
	return op, body, nil
}

func readFullTCP(conn net.Conn, buf []byte) (int, error) {
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

func avgFloat(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range data {
		sum += v
	}
	return sum / float64(len(data))
}

func percentileFloat(sorted []float64, p int) float64 {
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
