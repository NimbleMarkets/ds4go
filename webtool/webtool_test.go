package webtool

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWebHelperMock(t *testing.T) {
	fmt.Println("=== Starting Test ===")
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port
	fmt.Printf("Mock server listening on port %d\n", port)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mockCDPServer(t, l)
	}()

	cfg := Config{
		Port:            port,
		HomeDir:         t.TempDir(),
		ChromePath:      "mock-chrome",
		ConfirmApproval: func(message string) (bool, error) { return true, nil },
		Log: func(msg string) {
			fmt.Printf("[Config Log] %s\n", msg)
		},
	}

	w := NewWebHelper(cfg)
	w.browserAllowed = true

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fmt.Println("Calling GoogleSearch...")
	res, err := w.GoogleSearch(ctx, "hello")
	if err != nil {
		t.Fatalf("GoogleSearch failed: %v", err)
	}
	fmt.Printf("GoogleSearch returned: %q\n", res)

	expected := "Dummy markdown extraction result"
	if res != expected {
		t.Errorf("Expected %q, got %q", expected, res)
	}

	l.Close()
	wg.Wait()
	fmt.Println("=== Test Finished ===")
}

func mockCDPServer(t *testing.T, l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		fmt.Printf("Mock server accepted connection from %s\n", conn.RemoteAddr())
		go handleMockConn(t, conn, l.Addr().(*net.TCPAddr).Port)
	}
}

func handleMockConn(t *testing.T, conn net.Conn, port int) {
	defer func() {
		conn.Close()
		fmt.Println("Mock server connection handler exiting and closing connection")
	}()
	reader := bufio.NewReader(conn)

	req, err := http.ReadRequest(reader)
	if err != nil {
		fmt.Printf("Mock server ReadRequest error: %v\n", err)
		return
	}
	fmt.Printf("Mock server received request: %s %s\n", req.Method, req.URL.Path)

	if req.Method == "GET" && req.URL.Path == "/json/version" {
		resp := http.Response{
			StatusCode: 200,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     make(http.Header),
		}
		resp.Header.Set("Content-Type", "application/json")
		wsURL := fmt.Sprintf("ws://127.0.0.1:%d/devtools/browser", port)
		body := fmt.Sprintf(`{"webSocketDebuggerUrl": %q}`, wsURL)
		resp.Body = io.NopCloser(strings.NewReader(body))
		resp.ContentLength = int64(len(body))
		_ = resp.Write(conn)
		fmt.Println("Mock server responded to /json/version")
		return
	}

	if req.Method == "GET" && strings.HasPrefix(req.URL.Path, "/json/close/") {
		resp := http.Response{
			StatusCode: 200,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     make(http.Header),
		}
		body := "Target closed"
		resp.Body = io.NopCloser(strings.NewReader(body))
		resp.ContentLength = int64(len(body))
		_ = resp.Write(conn)
		fmt.Println("Mock server responded to /json/close")
		return
	}

	if strings.ToLower(req.Header.Get("Upgrade")) == "websocket" {
		fmt.Println("Mock server upgrading connection to WebSocket...")
		key := req.Header.Get("Sec-WebSocket-Key")
		hash := sha1.New()
		hash.Write([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		acceptKey := base64.StdEncoding.EncodeToString(hash.Sum(nil))

		respHead := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Accept: %s\r\n\r\n", acceptKey)
		_, _ = conn.Write([]byte(respHead))
		fmt.Println("Mock server sent WebSocket 101 response")

		isBrowserWS := req.URL.Path == "/devtools/browser"
		handleWSProtocol(t, conn, isBrowserWS)
	}
}

func handleWSProtocol(t *testing.T, conn net.Conn, isBrowserWS bool) {
	fmt.Printf("Starting handleWSProtocol, isBrowserWS=%t\n", isBrowserWS)
	for {
		header := make([]byte, 2)
		if _, err := io.ReadFull(conn, header); err != nil {
			fmt.Printf("handleWSProtocol ReadFull header error: %v\n", err)
			return
		}
		fin := (header[0] & 0x80) != 0
		opcode := header[0] & 0x0f
		masked := (header[1] & 0x80) != 0
		length := uint64(header[1] & 0x7f)

		if length == 126 {
			lenBytes := make([]byte, 2)
			_, _ = io.ReadFull(conn, lenBytes)
			length = uint64(lenBytes[0])<<8 | uint64(lenBytes[1])
		} else if length == 127 {
			lenBytes := make([]byte, 8)
			_, _ = io.ReadFull(conn, lenBytes)
			length = 0
			for i := 0; i < 8; i++ {
				length = (length << 8) | uint64(lenBytes[i])
			}
		}

		var mask []byte
		if masked {
			mask = make([]byte, 4)
			_, _ = io.ReadFull(conn, mask)
		}

		payload := make([]byte, length)
		_, _ = io.ReadFull(conn, payload)

		if masked {
			for i := uint64(0); i < length; i++ {
				payload[i] ^= mask[i%4]
			}
		}

		fmt.Printf("handleWSProtocol received frame: opcode=%d, payload=%s\n", opcode, string(payload))

		if opcode == 0x8 {
			fmt.Println("handleWSProtocol received Close frame")
			return
		}

		if opcode == 0x1 { // Text frame
			var req map[string]any
			if err := json.Unmarshal(payload, &req); err != nil {
				fmt.Printf("handleWSProtocol JSON unmarshal error: %v\n", err)
				continue
			}

			idVal, _ := req["id"].(float64)
			id := int(idVal)
			method, _ := req["method"].(string)
			fmt.Printf("handleWSProtocol parsed request: id=%d, method=%s\n", id, method)

			var respBody string
			if isBrowserWS {
				if method == "Target.createTarget" {
					respBody = fmt.Sprintf(`{"id":%d,"result":{"targetId":"dummy_target_id"}}`, id)
				}
			} else {
				switch method {
				case "Page.enable", "Runtime.enable", "Page.navigate":
					respBody = fmt.Sprintf(`{"id":%d,"result":{}}`, id)
				case "Emulation.setFocusEmulationEnabled", "Emulation.setDeviceMetricsOverride":
					respBody = fmt.Sprintf(`{"id":%d,"result":{}}`, id)
				case "Runtime.evaluate":
					params, _ := req["params"].(map[string]any)
					expr, _ := params["expression"].(string)
					var value string
					if expr == "document.readyState" {
						value = "complete"
					} else if strings.Contains(expr, "location.href+'\\n'") {
						value = "http://dummy.com\ncomplete\n100"
					} else if strings.Contains(expr, "accept all") {
						value = ""
					} else { // extraction JS
						value = "Dummy markdown extraction result"
					}
					respBody = fmt.Sprintf(`{"id":%d,"result":{"result":{"type":"string","value":%q}}}`, id, value)
				}
			}

			if respBody != "" {
				fmt.Printf("handleWSProtocol sending response: %s\n", respBody)
				var frame []byte
				frame = append(frame, 0x80|0x1) // FIN + text
				respBytes := []byte(respBody)
				respLen := len(respBytes)
				if respLen < 126 {
					frame = append(frame, byte(respLen))
				} else if respLen <= 0xffff {
					frame = append(frame, 126, byte(respLen>>8), byte(respLen))
				} else {
					frame = append(frame, 127)
					for i := 7; i >= 0; i-- {
						frame = append(frame, byte(respLen>>(i*8)))
					}
				}
				frame = append(frame, respBytes...)
				_, err := conn.Write(frame)
				if err != nil {
					fmt.Printf("handleWSProtocol Write response error: %v\n", err)
				}
			} else {
				fmt.Printf("handleWSProtocol no response generated for method %s\n", method)
			}
		}

		if !fin {
			continue
		}
	}
}
