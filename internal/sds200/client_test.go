package sds200

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type udpMockServer struct {
	conn     *net.UDPConn
	handler  func(req string) []string
	requests chan string
	done     chan struct{}
	wg       sync.WaitGroup
}

func startUDPMockServer(t *testing.T, handler func(req string) []string) *udpMockServer {
	t.Helper()

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	conn, err := net.ListenUDP("udp", addr)
	require.NoError(t, err)

	s := &udpMockServer{
		conn:     conn,
		handler:  handler,
		requests: make(chan string, 128),
		done:     make(chan struct{}),
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		buf := make([]byte, 64*1024)
		for {
			_ = s.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, remote, err := s.conn.ReadFromUDP(buf)
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					select {
					case <-s.done:
						return
					default:
						continue
					}
				}
				return
			}
			req := string(buf[:n])
			select {
			case s.requests <- req:
			default:
			}
			if s.handler == nil {
				continue
			}
			for _, resp := range s.handler(req) {
				_, _ = s.conn.WriteToUDP([]byte(resp), remote)
			}
		}
	}()

	t.Cleanup(func() {
		close(s.done)
		_ = s.conn.Close()
		s.wg.Wait()
	})
	return s
}

func (s *udpMockServer) hostPort() (string, int) {
	addr := s.conn.LocalAddr().(*net.UDPAddr)
	return addr.IP.String(), addr.Port
}

func readRequestCommand(req string) string {
	req = strings.TrimSpace(strings.TrimRight(req, "\r\n"))
	if req == "" {
		return ""
	}
	parts := strings.SplitN(req, ",", 2)
	return strings.ToUpper(parts[0])
}

func scannerInfoXML(mode string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?><ScannerInfo Mode="%s" V_Screen="conventional_scan"><Property VOL="11" SQL="5" Sig="3" Mute="Unmute"/><System Name="County"/><Department Name="Fire"/><ConvFrequency Name="Fire Ops" Freq="155.2200" Hold="On"/><Footer No="1" EOT="1"/></ScannerInfo>`, mode)
}

func gltXML() string {
	return `<?xml version="1.0" encoding="utf-8"?><GLT><FL Index="0" Name="Main"/><Footer No="1" EOT="1"/></GLT>`
}

func splitXMLResponse(command, xmlBody string) string {
	return command + ",<XML>," + xmlBody + "\r"
}

func newTestClient(t *testing.T, handler func(req string) []string) *Client {
	t.Helper()
	server := startUDPMockServer(t, handler)
	host, port := server.hostPort()
	client, err := NewClient(ClientConfig{
		Address:         host,
		ControlPort:     port,
		ResponseTimeout: 500 * time.Millisecond,
		Retries:         2,
		QueueSize:       32,
		ReadTimeout:     100 * time.Millisecond,
		WriteTimeout:    100 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestNewClient(t *testing.T) {
	t.Parallel()

	t.Run("missing_address", func(t *testing.T) {
		_, err := NewClient(ClientConfig{})
		require.Error(t, err)
	})

	t.Run("valid_config", func(t *testing.T) {
		server := startUDPMockServer(t, func(req string) []string {
			if readRequestCommand(req) == cmdMDL {
				return []string{"MDL,SDS200\r"}
			}
			return nil
		})
		host, port := server.hostPort()
		client, err := NewClient(ClientConfig{
			Address:     host,
			ControlPort: port,
		})
		require.NoError(t, err)
		defer func() { _ = client.Close() }()

		model, err := client.Model()
		require.NoError(t, err)
		assert.Equal(t, "SDS200", model)
	})
}

func TestClientTelemetrySnapshot(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(req string) []string { return nil })
	snap := client.TelemetrySnapshot()
	assert.False(t, snap.Connected)
}

func TestClientOnTelemetry(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(req string) []string {
		switch readRequestCommand(req) {
		case cmdMDL:
			return []string{
				"GST,00000,l1,m1,l2,m2,l3,m3,l4,m4,l5,m5,Mute,1,0,1,851.0125,NFM,0,851.0125,850.0000,852.0000,0,3\r",
				"MDL,SDS200\r",
			}
		default:
			return nil
		}
	})

	updates := make(chan RuntimeStatus, 1)
	client.OnTelemetry(func(status RuntimeStatus) {
		select {
		case updates <- status:
		default:
		}
	})

	_, err := client.Model()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	select {
	case got := <-updates:
		assert.Equal(t, "851.0125", got.Frequency)
	case <-ctx.Done():
		t.Fatal("expected telemetry callback")
	}
}

func TestClientProcessesUnsolicitedWhenIdle(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdPSI {
			return []string{splitXMLResponse("PSI", scannerInfoXML("Scan Mode"))}
		}
		return nil
	})

	updates := make(chan RuntimeStatus, 1)
	client.OnTelemetry(func(status RuntimeStatus) {
		select {
		case updates <- status:
		default:
		}
	})

	require.NoError(t, client.StartPushScannerInfo(0))

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	select {
	case got := <-updates:
		assert.True(t, got.Connected)
		assert.Equal(t, "Scan Mode", got.Mode)
		assert.Equal(t, "Fire Ops", got.Channel)
	case <-ctx.Done():
		t.Fatal("expected telemetry callback from unsolicited PSI")
	}
}

func TestClientOnRawResponse(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdMDL {
			return []string{"MDL,SDS200\r"}
		}
		return nil
	})

	rawResponses := make(chan CommandResponse, 1)
	client.OnRawResponse(func(resp CommandResponse) {
		select {
		case rawResponses <- resp:
		default:
		}
	})

	_, err := client.Model()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	select {
	case resp := <-rawResponses:
		assert.Equal(t, cmdMDL, resp.Command)
	case <-ctx.Done():
		t.Fatal("expected raw response callback")
	}
}

func TestClientResync(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(req string) []string {
		switch readRequestCommand(req) {
		case cmdMDL:
			return []string{"MDL,SDS200\r"}
		case cmdVER:
			return []string{"VER,1.02.03\r"}
		case cmdGSI:
			return []string{splitXMLResponse("GSI", scannerInfoXML("Scan Mode"))}
		default:
			return nil
		}
	})

	status, err := client.Resync()
	require.NoError(t, err)
	assert.True(t, status.Connected)
	assert.Equal(t, "Scan Mode", status.Mode)
	assert.Equal(t, "Fire Ops", status.Channel)
}

func TestClientClose(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(req string) []string { return nil })
	require.NoError(t, client.Close())
	_, err := client.Model()
	require.Error(t, err)
}
