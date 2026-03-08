package sds200

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type FTPConfig struct {
	Address  string
	Port     int
	Username string
	Password string
	Timeout  time.Duration
}

func (c FTPConfig) withDefaults() FTPConfig {
	if c.Port == 0 {
		c.Port = DefaultFTPPort
	}
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Second
	}
	return c
}

type FTPClient struct {
	cfg    FTPConfig
	conn   net.Conn
	r      *bufio.Reader
	w      *bufio.Writer
	closed bool
	mu     sync.Mutex
	opMu   sync.Mutex
}

func NewFTPClient(cfg FTPConfig) (*FTPClient, error) {
	cfg = cfg.withDefaults()
	if cfg.Address == "" {
		return nil, errors.New("ftp address is required")
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", cfg.Address, cfg.Port), cfg.Timeout)
	if err != nil {
		return nil, err
	}
	c := &FTPClient{cfg: cfg, conn: conn, r: bufio.NewReader(conn), w: bufio.NewWriter(conn)}
	if _, _, err := c.readResponse(); err != nil {
		_ = c.Close()
		return nil, err
	}
	if err := c.login(); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *FTPClient) Close() error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	_ = c.sendCommand("QUIT")
	return c.conn.Close()
}

func (c *FTPClient) List(path string) ([]string, error) {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	conn, err := c.openPassiveDataConn()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	cmd := "LIST"
	if strings.TrimSpace(path) != "" {
		cmd += " " + path
	}
	if err := c.sendCommand(cmd); err != nil {
		return nil, err
	} else if _, _, err := c.readResponse(); err != nil {
		return nil, err
	}

	payload, err := io.ReadAll(conn)
	if err != nil {
		return nil, err
	} else if _, _, err := c.readResponse(); err != nil {
		return nil, err
	}

	lines := strings.Split(strings.ReplaceAll(string(payload), "\r", ""), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

func (c *FTPClient) Retrieve(path string) ([]byte, error) {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	conn, err := c.openPassiveDataConn()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	if err := c.sendCommand("RETR " + path); err != nil {
		return nil, err
	} else if _, _, err := c.readResponse(); err != nil {
		return nil, err
	}
	payload, err := io.ReadAll(conn)
	if err != nil {
		return nil, err
	} else if _, _, err := c.readResponse(); err != nil {
		return nil, err
	}
	return payload, nil
}

func (c *FTPClient) Store(path string, content []byte) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	conn, err := c.openPassiveDataConn()
	if err != nil {
		return err
	}

	if err := c.sendCommand("STOR " + path); err != nil {
		_ = conn.Close()
		return err
	} else if _, _, err := c.readResponse(); err != nil {
		_ = conn.Close()
		return err
	} else if _, err := conn.Write(content); err != nil {
		_ = conn.Close()
		return err
	}
	// Signal EOF on data channel so server can finalize and return 226
	_ = conn.Close()
	if _, _, err := c.readResponse(); err != nil {
		return err
	}
	return nil
}

func (c *FTPClient) Delete(path string) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	if err := c.sendCommand("DELE " + path); err != nil {
		return err
	}
	_, _, err := c.readResponse()
	return err
}

func (c *FTPClient) MakeDir(path string) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	if err := c.sendCommand("MKD " + path); err != nil {
		return err
	}
	_, _, err := c.readResponse()
	return err
}

func (c *FTPClient) login() error {
	if err := c.sendCommand("USER " + c.cfg.Username); err != nil {
		return err
	} else if code, _, err := c.readResponse(); err != nil {
		return err
	} else if code == 331 {
		if err := c.sendCommand("PASS " + c.cfg.Password); err != nil {
			return err
		} else if _, _, err := c.readResponse(); err != nil {
			return err
		}
	}
	if err := c.sendCommand("TYPE I"); err != nil {
		return err
	}
	_, _, err := c.readResponse()
	return err
}

func (c *FTPClient) openPassiveDataConn() (net.Conn, error) {
	if err := c.sendCommand("PASV"); err != nil {
		return nil, err
	}
	_, line, err := c.readResponse()
	if err != nil {
		return nil, err
	}
	start := strings.Index(line, "(")
	end := strings.Index(line, ")")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("invalid pasv response: %s", line)
	}
	nums := strings.Split(line[start+1:end], ",")
	if len(nums) != 6 {
		return nil, fmt.Errorf("invalid pasv tuple: %s", line)
	}
	for i := range nums {
		nums[i] = strings.TrimSpace(nums[i])
	}
	host := strings.Join(nums[:4], ".")
	p1 := parseIntDefault(nums[4], 0)
	p2 := parseIntDefault(nums[5], 0)
	port := p1*256 + p2
	return net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), c.cfg.Timeout)
}

func (c *FTPClient) sendCommand(cmd string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errors.New("ftp client closed")
	} else if err := c.conn.SetDeadline(time.Now().Add(c.cfg.Timeout)); err != nil {
		return err
	} else if _, err := c.w.WriteString(cmd + "\r\n"); err != nil {
		return err
	}
	return c.w.Flush()
}

func (c *FTPClient) readResponse() (int, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return 0, "", errors.New("ftp client closed")
	}
	line, err := c.r.ReadString('\n')
	if err != nil {
		return 0, "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if len(line) < 3 {
		return 0, line, fmt.Errorf("invalid ftp response: %s", line)
	}
	code, err := strconv.Atoi(line[:3])
	if err != nil {
		return 0, line, err
	}
	if len(line) >= 4 && line[3] == '-' {
		prefix := line[:3] + " "
		for {
			next, err := c.r.ReadString('\n')
			if err != nil {
				return 0, "", err
			}
			next = strings.TrimRight(next, "\r\n")
			if strings.HasPrefix(next, prefix) {
				line = next
				break
			}
		}
	}
	if code >= 400 {
		return code, line, fmt.Errorf("ftp error %d: %s", code, line)
	}
	return code, line, nil
}
