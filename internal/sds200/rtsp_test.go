package sds200

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startRTSPMockServer(t *testing.T) (host string, port int) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		r := bufio.NewReader(conn)
		w := bufio.NewWriter(conn)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.Split(line, " ")
			if len(parts) < 3 {
				return
			}
			method := parts[0]

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

			cseq := headers["CSeq"]
			body := ""
			responseHeaders := map[string]string{
				"CSeq": cseq,
			}
			switch method {
			case "OPTIONS":
				responseHeaders["Public"] = "DESCRIBE, SETUP, TEARDOWN, PLAY, OPTIONS, GET_PARAMETER"
			case "DESCRIBE":
				body = "v=0\r\nm=audio 0 RTP/AVP 0\r\na=control:trackID=1\r\n"
				responseHeaders["Content-Type"] = "application/sdp"
				responseHeaders["Session"] = "abc123;timeout=60"
				responseHeaders["Content-Length"] = strconv.Itoa(len(body))
			case "SETUP":
				responseHeaders["Transport"] = "RTP/AVP;unicast;client_port=5000-5001;source=127.0.0.1;server_port=6000-6001;ssrc=1A2B3C4D"
				responseHeaders["Session"] = "abc123;timeout=60"
			case "PLAY":
				responseHeaders["Session"] = "abc123"
				responseHeaders["Range"] = "npt=0.0-596.48"
				responseHeaders["RTP-Info"] = "url=rtsp://127.0.0.1/au:scanner.au/trackID=1;seq=1;rtptime=0"
			case "GET_PARAMETER":
				responseHeaders["Session"] = "abc123"
			case "TEARDOWN":
				responseHeaders["Session"] = "abc123"
			}

			_, _ = w.WriteString("RTSP/1.0 200 OK\r\n")
			for k, v := range responseHeaders {
				_, _ = w.WriteString(k + ": " + v + "\r\n")
			}
			_, _ = w.WriteString("\r\n")
			if body != "" {
				_, _ = w.WriteString(body)
			}
			_ = w.Flush()
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

func TestRTSPClientOptions(t *testing.T) {
	t.Parallel()

	host, port := startRTSPMockServer(t)
	client, err := NewRTSPClient(RTSPConfig{Address: host, Port: port, Timeout: time.Second})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	resp, err := client.Options()
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, resp.Headers["Public"], "DESCRIBE")
}

func TestRTSPClientDescribe(t *testing.T) {
	t.Parallel()

	host, port := startRTSPMockServer(t)
	client, err := NewRTSPClient(RTSPConfig{Address: host, Port: port, Timeout: time.Second})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	resp, err := client.Describe()
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "application/sdp", resp.Headers["Content-Type"])
	assert.Contains(t, string(resp.Body), "m=audio")
}

func TestRTSPClientSetup(t *testing.T) {
	t.Parallel()

	host, port := startRTSPMockServer(t)
	client, err := NewRTSPClient(RTSPConfig{Address: host, Port: port, Timeout: time.Second})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	_, err = client.Describe()
	require.NoError(t, err)

	resp, err := client.Setup(5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, resp.Headers["Transport"], "server_port")
}

func TestRTSPClientPlay(t *testing.T) {
	t.Parallel()

	host, port := startRTSPMockServer(t)
	client, err := NewRTSPClient(RTSPConfig{Address: host, Port: port, Timeout: time.Second})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	_, err = client.Describe()
	require.NoError(t, err)
	_, err = client.Setup(5000)
	require.NoError(t, err)

	resp, err := client.Play()
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, resp.Headers["Rtp-Info"], "trackID=1")
}

func TestRTSPClientGetParameter(t *testing.T) {
	t.Parallel()

	host, port := startRTSPMockServer(t)
	client, err := NewRTSPClient(RTSPConfig{Address: host, Port: port, Timeout: time.Second})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	_, err = client.Describe()
	require.NoError(t, err)
	resp, err := client.GetParameter()
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestRTSPClientTeardown(t *testing.T) {
	t.Parallel()

	host, port := startRTSPMockServer(t)
	client, err := NewRTSPClient(RTSPConfig{Address: host, Port: port, Timeout: time.Second})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	_, err = client.Describe()
	require.NoError(t, err)
	resp, err := client.Teardown()
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}
