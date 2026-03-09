package ingest

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func unavailableRTSPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

func startIngestRTSPMockServer(t *testing.T) (host string, port int, serverRTPPort int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	srvRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srvRTP.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		r := bufio.NewReader(conn)
		w := bufio.NewWriter(conn)
		var clientPort int

		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			method := strings.Split(line, " ")[0]
			headers := map[string]string{}
			for {
				h, err := r.ReadString('\n')
				if err != nil {
					return
				}
				h = strings.TrimRight(h, "\r\n")
				if h == "" {
					break
				}
				idx := strings.Index(h, ":")
				if idx > 0 {
					headers[strings.TrimSpace(h[:idx])] = strings.TrimSpace(h[idx+1:])
				}
			}
			if method == "SETUP" {
				transport := headers["Transport"]
				idx := strings.Index(transport, "client_port=")
				if idx > 0 {
					ports := strings.SplitN(transport[idx+12:], "-", 2)
					clientPort, _ = strconv.Atoi(ports[0])
				}
			}
			cseq := headers["CSeq"]
			_, _ = w.WriteString("RTSP/1.0 200 OK\r\n")
			_, _ = w.WriteString("CSeq: " + cseq + "\r\n")
			switch method {
			case "OPTIONS":
				_, _ = w.WriteString("Public: DESCRIBE, SETUP, TEARDOWN, PLAY, OPTIONS, GET_PARAMETER\r\n")
			case "DESCRIBE":
				body := "v=0\r\nm=audio 0 RTP/AVP 0\r\na=control:trackID=1\r\n"
				_, _ = w.WriteString("Session: abc123;timeout=60\r\n")
				_, _ = w.WriteString("Content-Type: application/sdp\r\n")
				_, _ = w.WriteString("Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n")
				_, _ = w.WriteString(body)
				_ = w.Flush()
				continue
			case "SETUP":
				_, _ = fmt.Fprintf(
					w,
					"Transport: RTP/AVP;unicast;client_port=%d-%d;server_port=%d-%d\r\n",
					clientPort,
					clientPort+1,
					srvRTP.LocalAddr().(*net.UDPAddr).Port,
					srvRTP.LocalAddr().(*net.UDPAddr).Port+1,
				)
				_, _ = w.WriteString("Session: abc123;timeout=60\r\n")
			case "PLAY", "GET_PARAMETER", "TEARDOWN":
				_, _ = w.WriteString("Session: abc123\r\n")
			}
			_, _ = w.WriteString("\r\n")
			_ = w.Flush()

			if method == "PLAY" && clientPort > 0 {
				go func() {
					dst := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: clientPort}
					pkt := []byte{
						0x80, 0x00, 0x00, 0x01,
						0x00, 0x00, 0x00, 0x01,
						0x12, 0x34, 0x56, 0x78,
						0xFF, 0x7F, 0x00,
					}
					_, _ = srvRTP.WriteToUDP(pkt, dst)
				}()
			}
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port, srvRTP.LocalAddr().(*net.UDPAddr).Port
}

func TestParseRTPPayload(t *testing.T) {
	t.Parallel()

	t.Run("without_padding", func(t *testing.T) {
		pkt := []byte{0x80, 0x00, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0xFF, 0x7F}
		payload, err := parseRTPPayload(pkt)
		require.NoError(t, err)
		assert.Equal(t, []byte{0xFF, 0x7F}, payload)
	})

	t.Run("with_padding", func(t *testing.T) {
		pkt := []byte{
			0xA0, 0x00, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1,
			0xFF, 0x7F, 0x00, 0x02,
		}
		payload, err := parseRTPPayload(pkt)
		require.NoError(t, err)
		assert.Equal(t, []byte{0xFF, 0x7F}, payload)
	})

	t.Run("short_packet", func(t *testing.T) {
		pkt := make([]byte, 11)
		_, err := parseRTPPayload(pkt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "short")
	})

	t.Run("wrong_version", func(t *testing.T) {
		// Version 1 (bits 6-7 = 01)
		pkt := []byte{0x40, 0x00, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0xFF}
		_, err := parseRTPPayload(pkt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "version")
	})

	t.Run("wrong_payload_type", func(t *testing.T) {
		// PT=8
		pkt := []byte{0x80, 0x08, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0xFF}
		_, err := parseRTPPayload(pkt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "payload type")
	})

	t.Run("with_csrc", func(t *testing.T) {
		// CC=1 → 4 extra bytes after fixed header
		pkt := []byte{
			0x81, 0x00, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1,
			0xAA, 0xBB, 0xCC, 0xDD, // CSRC
			0xFF, 0x7F, // payload
		}
		payload, err := parseRTPPayload(pkt)
		require.NoError(t, err)
		assert.Equal(t, []byte{0xFF, 0x7F}, payload)
	})

	t.Run("with_extension", func(t *testing.T) {
		// X bit set, extension header with 1 word (4 bytes) of data
		pkt := []byte{
			0x90, 0x00, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1,
			0x00, 0x01, 0x00, 0x01, // ext profile + length (1 word)
			0xDE, 0xAD, 0xBE, 0xEF, // ext data
			0xFF, 0x7F, // payload
		}
		payload, err := parseRTPPayload(pkt)
		require.NoError(t, err)
		assert.Equal(t, []byte{0xFF, 0x7F}, payload)
	})

	t.Run("empty_payload", func(t *testing.T) {
		// Valid 12-byte header, no payload bytes
		pkt := []byte{0x80, 0x00, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1}
		_, err := parseRTPPayload(pkt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty")
	})

	t.Run("invalid_padding", func(t *testing.T) {
		// P bit set, pad length exceeds available bytes
		pkt := []byte{
			0xA0, 0x00, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1,
			0xFF, 0x10, // pad length 0x10 = 16, but only 2 payload bytes
		}
		_, err := parseRTPPayload(pkt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "padding")
	})
}

func TestExtractRTPTimestamp(t *testing.T) {
	t.Parallel()

	t.Run("valid_packet", func(t *testing.T) {
		pkt := []byte{0x80, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x12, 0x34, 0x56, 0x78, 0xFF}
		assert.Equal(t, uint32(1), extractRTPTimestamp(pkt))
	})

	t.Run("short_packet", func(t *testing.T) {
		pkt := []byte{0x80, 0x00, 0x00, 0x01}
		assert.Equal(t, uint32(0), extractRTPTimestamp(pkt))
	})

	t.Run("large_timestamp", func(t *testing.T) {
		pkt := []byte{0x80, 0x00, 0x00, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0x12, 0x34, 0x56, 0x78}
		assert.Equal(t, uint32(0xFFFFFFFF), extractRTPTimestamp(pkt))
	})
}

func TestUlawToPCM16(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    byte
		expected int16
	}{
		{"positive_silence", 0xFF, 0},
		{"negative_silence", 0x7F, 0},
		{"positive_near_max", 0x80, 32124},
		{"negative_near_max", 0x00, -32124},
		{"mid_range_positive", 0xF7, 64},
		{"mid_range_negative", 0x77, -64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			samples := decodeULaw([]byte{tt.input})
			require.Len(t, samples, 1)
			assert.Equal(t, tt.expected, samples[0])
		})
	}
}

func TestIngestSessionReceivesFrame(t *testing.T) {
	t.Parallel()

	host, port, _ := startIngestRTSPMockServer(t)
	frames := make(chan Frame, 1)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s, err := NewSession(ctx, Config{
		Address:           host,
		RTSPPort:          port,
		ReconnectDelay:    100 * time.Millisecond,
		MaxReconnectFails: 2,
		OnFrame: func(f Frame) {
			select {
			case frames <- f:
			default:
			}
		},
	})
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	select {
	case f := <-frames:
		// Mock sends {0xFF, 0x7F, 0x00}
		assert.Equal(t, []int16{0, 0, -32124}, f.Samples)
		assert.False(t, f.ReceivedAt.IsZero())
		assert.Equal(t, uint32(1), f.RTPTimestamp)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "expected decoded frame")
	}
}

func TestReconnectBackoffDelay(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 2*time.Second, reconnectBackoffDelay(2*time.Second, 1))
	assert.Equal(t, 4*time.Second, reconnectBackoffDelay(2*time.Second, 2))
	assert.Equal(t, 8*time.Second, reconnectBackoffDelay(2*time.Second, 3))
	assert.Equal(t, 16*time.Second, reconnectBackoffDelay(2*time.Second, 4))
	assert.Equal(t, 30*time.Second, reconnectBackoffDelay(2*time.Second, 5))
	assert.Equal(t, 30*time.Second, reconnectBackoffDelay(2*time.Second, 20))
	assert.Equal(t, 2*time.Second, reconnectBackoffDelay(0, 1))
}

func TestIngestSessionReconnectBudgetHandling(t *testing.T) {
	t.Parallel()

	t.Run("bounded_budget_emits_fatal", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		s, err := NewSession(ctx, Config{
			Address:           "127.0.0.1",
			RTSPPort:          unavailableRTSPPort(t),
			ReconnectDelay:    5 * time.Millisecond,
			MaxReconnectFails: 2,
		})
		require.NoError(t, err)
		defer func() { _ = s.Close() }()

		select {
		case fatalErr := <-s.Fatal():
			require.Error(t, fatalErr)
			assert.Contains(t, fatalErr.Error(), "audio ingest reconnect budget exceeded")
			assert.Contains(t, fatalErr.Error(), "after 2 consecutive failures")
		case <-time.After(2 * time.Second):
			require.FailNow(t, "expected reconnect budget fatal")
		}
	})

	t.Run("unbounded_budget_keeps_retrying", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		s, err := NewSession(ctx, Config{
			Address:           "127.0.0.1",
			RTSPPort:          unavailableRTSPPort(t),
			ReconnectDelay:    5 * time.Millisecond,
			MaxReconnectFails: 0,
		})
		require.NoError(t, err)
		defer func() { _ = s.Close() }()

		select {
		case fatalErr := <-s.Fatal():
			require.FailNowf(t, "unexpected fatal", "received fatal in unbounded mode: %v", fatalErr)
		case <-time.After(150 * time.Millisecond):
		}
	})
}
