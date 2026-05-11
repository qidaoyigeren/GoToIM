// e2e_test_client is a command-line tool for end-to-end verification of GoIM.
// It connects via TCP, authenticates, and provides interactive commands
// to test session, ACK, sync, and push functionality.
//
// Usage:
//
//	go run examples/e2e_test_client/main.go -mid 1001 -device phone-001 -platform android
//	go run examples/e2e_test_client/main.go -mid 1002 -device phone-002 -platform ios -addr 127.0.0.1:3101
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"
)

const (
	rawHeaderLen = 16
	// Op codes
	opHeartbeat      = int32(2)
	opHeartbeatReply = int32(3)
	opSendMsg        = int32(4)
	opAuth           = int32(7)
	opAuthReply      = int32(8)
	opRaw            = int32(9)
	opSendMsgAck     = int32(18)
	opPushMsgAck     = int32(19)
	opSyncReq        = int32(20)
	opSyncReply      = int32(21)
)

var (
	addr     string
	mid      int64
	key      string
	deviceID string
	platform string
	roomID   string
	accepts  string
)

func init() {
	flag.StringVar(&addr, "addr", "127.0.0.1:3101", "comet TCP addr")
	flag.Int64Var(&mid, "mid", 1001, "user member id")
	flag.StringVar(&key, "key", "", "connection key (auto-generated if empty)")
	flag.StringVar(&deviceID, "device", "phone-001", "device id")
	flag.StringVar(&platform, "platform", "android", "platform (web/android/ios)")
	flag.StringVar(&roomID, "room", "live://1000", "room id")
	flag.StringVar(&accepts, "accepts", "1000,1001,1002", "comma-separated op codes to subscribe")
}

func main() {
	flag.Parse()

	if key == "" {
		key = fmt.Sprintf("e2e-%d-%d", mid, time.Now().UnixNano()%10000)
	}

	fmt.Printf("=== GoIM E2E Test Client (TCP) ===\n")
	fmt.Printf("  mid:       %d\n", mid)
	fmt.Printf("  key:       %s\n", key)
	fmt.Printf("  device:    %s\n", deviceID)
	fmt.Printf("  platform:  %s\n", platform)
	fmt.Printf("  room:      %s\n", roomID)
	fmt.Printf("  addr:      %s\n", addr)
	fmt.Println()

	// Connect
	fmt.Printf("[connect] tcp://%s\n", addr)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		logf("dial: %v", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Println("[connected]")

	// Auth
	acceptOps := parseAccepts(accepts)
	token := map[string]interface{}{
		"mid":       mid,
		"key":       key,
		"room_id":   roomID,
		"platform":  platform,
		"device_id": deviceID,
		"accepts":   acceptOps,
	}
	tokenBytes, _ := json.Marshal(token)
	writePacket(conn, opAuth, 1, tokenBytes)
	fmt.Printf("[auth sent] %s\n", tokenBytes)

	// Wait for auth reply
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	authReply, err := readPacket(conn)
	if err != nil {
		logf("read auth reply: %v", err)
		os.Exit(1)
	}
	conn.SetReadDeadline(time.Time{})
	if authReply.Op == opAuthReply {
		fmt.Println("[auth OK]")
	} else {
		fmt.Printf("[auth unexpected] op=%d\n", authReply.Op)
	}
	fmt.Println()

	// Read pump
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			pkt, err := readPacket(conn)
			if err != nil {
				if err != io.EOF {
					fmt.Printf("[read error] %v\n", err)
				}
				return
			}
			handlePacket(pkt)
		}
	}()

	// Heartbeat ticker
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := writePacket(conn, opHeartbeat, 1, nil); err != nil {
				return
			}
			fmt.Printf("[%s] >> heartbeat\n", ts())
		}
	}()

	// Handle Ctrl+C
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	go func() {
		<-interrupt
		fmt.Println("\n[interrupted]")
		conn.Close()
		wg.Wait()
		os.Exit(0)
	}()

	// Interactive commands
	printHelp()

	scanner := newLineScanner()
	for {
		line := scanner.nextLine()
		if line == "" {
			continue
		}

		parts := splitN(line, 4)
		cmd := parts[0]

		switch cmd {
		case "ack":
			msgID := "test-msg"
			seq := int64(1)
			if len(parts) > 1 {
				msgID = parts[1]
			}
			if len(parts) > 2 {
				fmt.Sscanf(parts[2], "%d", &seq)
			}
			body := marshalAckBody(msgID, seq)
			if err := writePacket(conn, opPushMsgAck, 1, body); err != nil {
				fmt.Printf("[error] %v\n", err)
			} else {
				fmt.Printf("[%s] >> ACK msg_id=%s seq=%d\n", ts(), msgID, seq)
			}

		case "sync":
			lastSeq := int64(0)
			limit := int32(100)
			if len(parts) > 1 {
				fmt.Sscanf(parts[1], "%d", &lastSeq)
			}
			if len(parts) > 2 {
				var l int32
				fmt.Sscanf(parts[2], "%d", &l)
				if l > 0 {
					limit = l
				}
			}
			body := make([]byte, 12)
			binary.BigEndian.PutUint64(body[0:8], uint64(lastSeq))
			binary.BigEndian.PutUint32(body[8:12], uint32(limit))
			if err := writePacket(conn, opSyncReq, 1, body); err != nil {
				fmt.Printf("[error] %v\n", err)
			} else {
				fmt.Printf("[%s] >> SYNC last_seq=%d limit=%d\n", ts(), lastSeq, limit)
			}

		case "push":
			if len(parts) < 3 {
				fmt.Println("Usage: push <op> <body>")
				continue
			}
			var op int32
			fmt.Sscanf(parts[1], "%d", &op)
			if err := writePacket(conn, op, 1, []byte(parts[2])); err != nil {
				fmt.Printf("[error] %v\n", err)
			} else {
				fmt.Printf("[%s] >> PUSH op=%d body=%s\n", ts(), op, parts[2])
			}

		case "sub":
			if len(parts) < 2 {
				fmt.Println("Usage: sub <op1,op2,...>")
				continue
			}
			if err := writePacket(conn, 14, 1, []byte(parts[1])); err != nil {
				fmt.Printf("[error] %v\n", err)
			} else {
				fmt.Printf("[%s] >> SUB ops=%s\n", ts(), parts[1])
			}

		case "help":
			printHelp()

		case "quit", "exit":
			fmt.Println("[bye]")
			conn.Close()
			wg.Wait()
			return

		default:
			fmt.Printf("Unknown command: %s (type 'help' for commands)\n", cmd)
		}
	}
}

// ============ Packet I/O ============

type Packet struct {
	Ver  int32
	Op   int32
	Seq  int32
	Body []byte
}

func readPacket(r io.Reader) (*Packet, error) {
	hdr := make([]byte, rawHeaderLen)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, err
	}
	packLen := int32(binary.BigEndian.Uint32(hdr[0:4]))
	headerLen := int16(binary.BigEndian.Uint16(hdr[4:6]))
	ver := int16(binary.BigEndian.Uint16(hdr[6:8]))
	op := int32(binary.BigEndian.Uint32(hdr[8:12]))
	seq := int32(binary.BigEndian.Uint32(hdr[12:16]))

	var body []byte
	if int(packLen) > int(headerLen) {
		bodyLen := int(packLen) - int(headerLen)
		body = make([]byte, bodyLen)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, err
		}
	}
	return &Packet{Ver: int32(ver), Op: op, Seq: seq, Body: body}, nil
}

func writePacket(w io.Writer, op int32, seq int32, body []byte) error {
	packLen := rawHeaderLen + len(body)
	buf := make([]byte, packLen)
	binary.BigEndian.PutUint32(buf[0:4], uint32(packLen))
	binary.BigEndian.PutUint16(buf[4:6], uint16(rawHeaderLen))
	binary.BigEndian.PutUint16(buf[6:8], 1) // version
	binary.BigEndian.PutUint32(buf[8:12], uint32(op))
	binary.BigEndian.PutUint32(buf[12:16], uint32(seq))
	if body != nil {
		copy(buf[rawHeaderLen:], body)
	}
	_, err := w.Write(buf)
	return err
}

func handlePacket(p *Packet) {
	switch p.Op {
	case opAuthReply:
		fmt.Printf("[%s] << AUTH_REPLY\n", ts())
	case opHeartbeatReply:
		online := int32(0)
		if len(p.Body) >= 4 {
			online = int32(binary.BigEndian.Uint32(p.Body[0:4]))
		}
		fmt.Printf("[%s] << HEARTBEAT_REPLY online=%d\n", ts(), online)
	case opSendMsgAck:
		fmt.Printf("[%s] << SEND_MSG_ACK seq=%d body=%q\n", ts(), p.Seq, p.Body)
	case opSyncReply:
		fmt.Printf("[%s] << SYNC_REPLY seq=%d body_len=%d\n", ts(), p.Seq, len(p.Body))
		if len(p.Body) >= 13 {
			currentSeq := int64(binary.BigEndian.Uint64(p.Body[0:8]))
			hasMore := p.Body[8] == 1
			msgCount := int(binary.BigEndian.Uint32(p.Body[9:13]))
			fmt.Printf("         current_seq=%d has_more=%v msg_count=%d\n", currentSeq, hasMore, msgCount)
			// Decode each message
			offset := 13
			for i := 0; i < msgCount; i++ {
				if offset+4 > len(p.Body) {
					break
				}
				msgLen := int(binary.BigEndian.Uint32(p.Body[offset : offset+4]))
				offset += 4
				if offset+msgLen > len(p.Body) {
					break
				}
				// Decode MsgBody
				msgData := p.Body[offset : offset+msgLen]
				if len(msgData) >= 2 {
					idLen := int(binary.BigEndian.Uint16(msgData[0:2]))
					if 2+idLen <= len(msgData) {
						msgID := string(msgData[2 : 2+idLen])
						fmt.Printf("         [%d] msg_id=%s\n", i, msgID)
					}
				}
				offset += msgLen
			}
		}
	case opRaw:
		fmt.Printf("[%s] << RAW body=%q\n", ts(), p.Body)
	default:
		fmt.Printf("[%s] << op=%d ver=%d seq=%d body=%q\n", ts(), p.Op, p.Ver, p.Seq, p.Body)
	}
}

// ============ ACK Body Encoding ============

func marshalAckBody(msgID string, seq int64) []byte {
	idBytes := []byte(msgID)
	buf := make([]byte, 2+len(idBytes)+8)
	binary.BigEndian.PutUint16(buf[0:2], uint16(len(idBytes)))
	copy(buf[2:], idBytes)
	binary.BigEndian.PutUint64(buf[2+len(idBytes):], uint64(seq))
	return buf
}

// ============ Helpers ============

func printHelp() {
	fmt.Println("Commands:")
	fmt.Println("  ack <msg_id> [seq]       - send ACK for a message (op=19)")
	fmt.Println("  sync [last_seq] [limit]  - request offline sync (op=20)")
	fmt.Println("  push <op> <body>         - send a raw message")
	fmt.Println("  sub <op1,op2,...>        - subscribe to op codes")
	fmt.Println("  help                     - show this help")
	fmt.Println("  quit                     - exit")
	fmt.Println()
}

func parseAccepts(s string) []int32 {
	var ops []int32
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var op int32
		fmt.Sscanf(part, "%d", &op)
		ops = append(ops, op)
	}
	return ops
}

func ts() string { return time.Now().Format("15:04:05") }

func logf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func splitN(s string, n int) []string {
	var parts []string
	for i := 0; i < n-1; i++ {
		idx := strings.IndexByte(s, ' ')
		if idx < 0 {
			break
		}
		parts = append(parts, s[:idx])
		s = strings.TrimSpace(s[idx+1:])
	}
	parts = append(parts, s)
	return parts
}

// lineScanner reads lines from stdin without bufio dependency
type lineScanner struct {
	buf []byte
}

func newLineScanner() *lineScanner {
	return &lineScanner{}
}

func (s *lineScanner) nextLine() string {
	// Read one byte at a time until newline
	s.buf = s.buf[:0]
	tmp := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(tmp)
		if n > 0 {
			if tmp[0] == '\n' {
				return strings.TrimSpace(string(s.buf))
			}
			if tmp[0] != '\r' {
				s.buf = append(s.buf, tmp[0])
			}
		}
		if err != nil {
			return strings.TrimSpace(string(s.buf))
		}
	}
}
