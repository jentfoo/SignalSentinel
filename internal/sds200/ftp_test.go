package sds200

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startFTPMockServer(t *testing.T) (host string, port int, stored *string) {
	t.Helper()

	var storedValue string
	stored = &storedValue

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
		_, _ = w.WriteString("220 mock ftp\r\n")
		_ = w.Flush()

		var dataLn net.Listener
		var dataPort int

		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			parts := strings.SplitN(line, " ", 2)
			cmd := strings.ToUpper(parts[0])
			arg := ""
			if len(parts) > 1 {
				arg = parts[1]
			}

			switch cmd {
			case "USER":
				_, _ = w.WriteString("331 password required\r\n")
			case "PASS":
				_, _ = w.WriteString("230 login ok\r\n")
			case "TYPE":
				_, _ = w.WriteString("200 type set\r\n")
			case "PASV":
				if dataLn != nil {
					_ = dataLn.Close()
				}
				dataLn, _ = net.Listen("tcp", "127.0.0.1:0")
				dp := dataLn.Addr().(*net.TCPAddr).Port
				dataPort = dp
				p1 := dp / 256
				p2 := dp % 256
				_, _ = fmt.Fprintf(w, "227 entering passive mode (127,0,0,1,%d,%d)\r\n", p1, p2)
			case "LIST":
				_, _ = w.WriteString("150 opening data\r\n")
				_ = w.Flush()
				dc, _ := dataLn.Accept()
				_, _ = io.WriteString(dc, "-rw-r--r-- 1 root root 4 test.txt\r\n")
				_ = dc.Close()
				_, _ = w.WriteString("226 transfer complete\r\n")
				_ = dataLn.Close()
				dataLn = nil
				_ = dataPort
			case "RETR":
				_, _ = w.WriteString("150 opening data\r\n")
				_ = w.Flush()
				dc, _ := dataLn.Accept()
				_, _ = io.WriteString(dc, "file-content")
				_ = dc.Close()
				_, _ = w.WriteString("226 transfer complete\r\n")
				_ = dataLn.Close()
				dataLn = nil
			case "STOR":
				_, _ = w.WriteString("150 opening data\r\n")
				_ = w.Flush()
				dc, _ := dataLn.Accept()
				payload, _ := io.ReadAll(dc)
				storedValue = string(payload)
				_ = dc.Close()
				_, _ = w.WriteString("226 transfer complete\r\n")
				_ = dataLn.Close()
				dataLn = nil
			case "DELE":
				_ = arg
				_, _ = w.WriteString("250 deleted\r\n")
			case "MKD":
				_, _ = w.WriteString("257 created\r\n")
			case "QUIT":
				_, _ = w.WriteString("221 bye\r\n")
				_ = w.Flush()
				return
			default:
				_, _ = w.WriteString("200 ok\r\n")
			}
			_ = w.Flush()
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port, stored
}

func TestNewFTPClient(t *testing.T) {
	t.Parallel()

	host, port, _ := startFTPMockServer(t)
	client, err := NewFTPClient(FTPConfig{
		Address:  host,
		Port:     port,
		Username: "user",
		Password: "pass",
		Timeout:  time.Second,
	})
	require.NoError(t, err)
	require.NoError(t, client.Close())
}

func TestFTPClientList(t *testing.T) {
	t.Parallel()

	host, port, _ := startFTPMockServer(t)
	client, err := NewFTPClient(FTPConfig{
		Address:  host,
		Port:     port,
		Username: "user",
		Password: "pass",
		Timeout:  time.Second,
	})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	list, err := client.List("/")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Contains(t, list[0], "test.txt")
}

func TestFTPClientRetrieve(t *testing.T) {
	t.Parallel()

	host, port, _ := startFTPMockServer(t)
	client, err := NewFTPClient(FTPConfig{
		Address:  host,
		Port:     port,
		Username: "user",
		Password: "pass",
		Timeout:  time.Second,
	})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	payload, err := client.Retrieve("/test.txt")
	require.NoError(t, err)
	assert.Equal(t, "file-content", string(payload))
}

func TestFTPClientStore(t *testing.T) {
	t.Parallel()

	host, port, stored := startFTPMockServer(t)
	client, err := NewFTPClient(FTPConfig{
		Address:  host,
		Port:     port,
		Username: "user",
		Password: "pass",
		Timeout:  time.Second,
	})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	require.NoError(t, client.Store("/upload.txt", []byte("payload")))
	assert.Equal(t, "payload", *stored)
}

func TestFTPClientDelete(t *testing.T) {
	t.Parallel()

	host, port, _ := startFTPMockServer(t)
	client, err := NewFTPClient(FTPConfig{
		Address:  host,
		Port:     port,
		Username: "user",
		Password: "pass",
		Timeout:  time.Second,
	})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	require.NoError(t, client.Delete("/dead.txt"))
}

func TestFTPClientMakeDir(t *testing.T) {
	t.Parallel()

	host, port, _ := startFTPMockServer(t)
	client, err := NewFTPClient(FTPConfig{
		Address:  host,
		Port:     port,
		Username: "user",
		Password: "pass",
		Timeout:  time.Second,
	})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	require.NoError(t, client.MakeDir("/newdir"))
}
