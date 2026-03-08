package sds200

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUDPTransport(t *testing.T) {
	t.Parallel()

	t.Run("missing_address", func(t *testing.T) {
		_, err := NewUDPTransport(TransportConfig{})
		require.Error(t, err)
	})

	t.Run("valid_address", func(t *testing.T) {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		require.NoError(t, err)
		defer func() { _ = pc.Close() }()
		addr := pc.LocalAddr().(*net.UDPAddr)

		tr, err := NewUDPTransport(TransportConfig{
			Address: addr.IP.String(),
			Port:    addr.Port,
		})
		require.NoError(t, err)
		require.NoError(t, tr.Close())
	})
}

func TestUDPTransportSend(t *testing.T) {
	t.Parallel()

	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = server.Close() }()
	addr := server.LocalAddr().(*net.UDPAddr)

	tr, err := NewUDPTransport(TransportConfig{
		Address:      addr.IP.String(),
		Port:         addr.Port,
		ReadTimeout:  50 * time.Millisecond,
		WriteTimeout: time.Second,
	})
	require.NoError(t, err)
	defer func() { _ = tr.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, tr.Send(ctx, []byte("MDL\r")))

	buf := make([]byte, 1024)
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := server.ReadFrom(buf)
	require.NoError(t, err)
	assert.Equal(t, "MDL\r", string(buf[:n]))
}

func TestUDPTransportReceive(t *testing.T) {
	t.Parallel()

	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = server.Close() }()
	addr := server.LocalAddr().(*net.UDPAddr)

	tr, err := NewUDPTransport(TransportConfig{
		Address:      addr.IP.String(),
		Port:         addr.Port,
		ReadTimeout:  50 * time.Millisecond,
		WriteTimeout: time.Second,
	})
	require.NoError(t, err)
	defer func() { _ = tr.Close() }()

	go func() {
		buf := make([]byte, 1024)
		n, remote, readErr := server.ReadFrom(buf)
		if readErr != nil {
			return
		}
		_, _ = server.WriteTo([]byte("MDL,SDS200\r"), remote)
		_ = n
	}()

	ctxSend, cancelSend := context.WithTimeout(t.Context(), time.Second)
	defer cancelSend()
	require.NoError(t, tr.Send(ctxSend, []byte("MDL\r")))

	ctxRecv, cancelRecv := context.WithTimeout(t.Context(), time.Second)
	defer cancelRecv()
	msg, err := tr.Receive(ctxRecv)
	require.NoError(t, err)
	assert.Equal(t, "MDL,SDS200\r", string(msg))
}

func TestUDPTransportClose(t *testing.T) {
	t.Parallel()

	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = server.Close() }()
	addr := server.LocalAddr().(*net.UDPAddr)

	tr, err := NewUDPTransport(TransportConfig{
		Address: addr.IP.String(),
		Port:    addr.Port,
	})
	require.NoError(t, err)
	require.NoError(t, tr.Close())

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err = tr.Send(ctx, []byte("MDL\r"))
	require.Error(t, err)
}
