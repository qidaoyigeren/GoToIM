package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	opHeartbeat      = 2
	opHeartbeatReply = 3
	opAuth           = 7
	opAuthReply      = 8
	opRaw            = 9
)

func main() {
	wsHost := "localhost:3102"
	apiHost := "http://localhost:8080"
	if len(os.Args) > 1 {
		wsHost = os.Args[1]
	}

	fmt.Println("=== GoIM E2E Test ===")
	fmt.Printf("WS: ws://%s/sub\n", wsHost)
	fmt.Printf("API: %s\n", apiHost)

	// Connect Alice
	fmt.Println("\n--- Connecting Alice (mid=1001) ---")
	aliceConn, aliceKey := connect(wsHost, 1001)
	defer aliceConn.Close()
	fmt.Printf("Alice connected with key: %s\n", aliceKey)

	// Connect Bob
	fmt.Println("\n--- Connecting Bob (mid=1002) ---")
	bobConn, bobKey := connect(wsHost, 1002)
	defer bobConn.Close()
	fmt.Printf("Bob connected with key: %s\n", bobKey)

	// Wait for mappings to be stored
	time.Sleep(500 * time.Millisecond)

	// Alice sends message to Bob via HTTP API
	fmt.Println("\n--- Alice sends message to Bob ---")
	testMsg := fmt.Sprintf(`{"from":1001,"to":1002,"text":"Hello Bob! E2E Test","ts":%d}`, time.Now().UnixMilli())
	resp, err := http.Post(
		fmt.Sprintf("%s/goim/push/mids?operation=9&mids=1002", apiHost),
		"application/json",
		strings.NewReader(testMsg),
	)
	if err != nil {
		fmt.Printf("HTTP push failed: %v\n", err)
		os.Exit(1)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fmt.Printf("Push API response: %s\n", body)

	// Wait for Kafka -> Worker -> Comet -> WebSocket delivery
	fmt.Println("\n--- Waiting for message delivery ---")

	// Read from Bob's WebSocket with timeout
	bobConn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for i := 0; i < 5; i++ {
		_, data, err := bobConn.ReadMessage()
		if err != nil {
			if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
				fmt.Println("Timeout waiting for message on Bob's connection")
			} else {
				fmt.Printf("Bob read error: %v\n", err)
			}
			break
		}
		if len(data) < 16 {
			fmt.Printf("Short message: %d bytes\n", len(data))
			continue
		}
		op := int32(binary.BigEndian.Uint32(data[8:12]))
		fmt.Printf("Bob received: op=%d len=%d\n", op, len(data))
		if op == opRaw {
			msgBody := string(data[16:])
			fmt.Printf("Bob received RAW body: %s\n", msgBody)
			fmt.Println("\n*** E2E TEST PASSED! Bob received the message! ***")
			return
		}
		if op == opAuthReply {
			fmt.Println("(auth reply)")
		}
	}

	fmt.Println("\n*** E2E TEST FAILED! Bob did not receive the message! ***")

	// Also check Alice
	aliceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 3; i++ {
		_, data, err := aliceConn.ReadMessage()
		if err != nil {
			break
		}
		if len(data) >= 16 {
			op := int32(binary.BigEndian.Uint32(data[8:12]))
			if op == opRaw {
				fmt.Printf("Alice received RAW: %s\n", string(data[16:]))
			}
		}
	}
	os.Exit(1)
}

func connect(host string, mid int64) (*websocket.Conn, string) {
	conn, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://%s/sub", host), nil)
	if err != nil {
		fmt.Printf("Connect failed: %v\n", err)
		os.Exit(1)
	}

	key := fmt.Sprintf("e2e-%d-%d", mid, time.Now().UnixNano())
	token := fmt.Sprintf(`{"mid":%d,"key":"%s","room_id":"live://1000","platform":"e2e","device_id":"e2e-dev-%d","accepts":[%d]}`,
		mid, key, mid, opRaw)

	// Build auth proto: packLen(4) headerLen(2) ver(2) op(4) seq(4) body
	tokenBytes := []byte(token)
	buf := make([]byte, 16+len(tokenBytes))
	binary.BigEndian.PutUint32(buf[0:4], uint32(16+len(tokenBytes)))
	binary.BigEndian.PutUint16(buf[4:6], 16)
	binary.BigEndian.PutUint16(buf[6:8], 1)
	binary.BigEndian.PutUint32(buf[8:12], uint32(opAuth))
	binary.BigEndian.PutUint32(buf[12:16], 1)
	copy(buf[16:], tokenBytes)

	if err := conn.WriteMessage(websocket.BinaryMessage, buf); err != nil {
		fmt.Printf("Auth write failed: %v\n", err)
		os.Exit(1)
	}

	// Read auth reply
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		fmt.Printf("Auth read failed: %v\n", err)
		os.Exit(1)
	}
	if len(data) >= 12 {
		op := int32(binary.BigEndian.Uint32(data[8:12]))
		if op != opAuthReply {
			fmt.Printf("Expected auth reply (op=8), got op=%d\n", op)
			os.Exit(1)
		}
	}

	return conn, key
}
