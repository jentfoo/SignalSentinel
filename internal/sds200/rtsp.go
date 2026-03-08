package sds200

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RTSPConfig struct {
	Address           string
	Port              int
	Path              string
	Timeout           time.Duration
	KeepAliveInterval time.Duration
}

func (c RTSPConfig) withDefaults() RTSPConfig {
	if c.Port == 0 {
		c.Port = DefaultRTSPPort
	}
	if c.Path == "" {
		c.Path = "au:scanner.au"
	}
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Second
	}
	if c.KeepAliveInterval == 0 {
		c.KeepAliveInterval = 45 * time.Second
	}
	return c
}

func (c RTSPConfig) URL() string {
	cfg := c.withDefaults()
	return fmt.Sprintf("rtsp://%s:%d/%s", cfg.Address, cfg.Port, cfg.Path)
}

func (c RTSPConfig) TrackURL(trackID int) string {
	if trackID <= 0 {
		trackID = 1
	}
	return c.URL() + fmt.Sprintf("/trackID=%d", trackID)
}

type RTSPResponse struct {
	StatusCode int
	Status     string
	Headers    map[string]string
	Body       []byte
}

type RTSPClient struct {
	cfg        RTSPConfig
	conn       net.Conn
	reader     *bufio.Reader
	writer     *bufio.Writer
	cseq       int
	sessionID  string
	sessionRaw string
	mu         sync.Mutex
}

func NewRTSPClient(cfg RTSPConfig) (*RTSPClient, error) {
	cfg = cfg.withDefaults()
	if cfg.Address == "" {
		return nil, errors.New("rtsp address is required")
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", cfg.Address, cfg.Port), cfg.Timeout)
	if err != nil {
		return nil, err
	}
	return &RTSPClient{
		cfg:    cfg,
		conn:   conn,
		reader: bufio.NewReader(conn),
		writer: bufio.NewWriter(conn),
		cseq:   1,
	}, nil
}

func (c *RTSPClient) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *RTSPClient) Options() (RTSPResponse, error) {
	return c.do("OPTIONS", c.cfg.URL(), nil, nil)
}

func (c *RTSPClient) Describe() (RTSPResponse, error) {
	headers := map[string]string{"Accept": "application/sdp"}
	resp, err := c.do("DESCRIBE", c.cfg.URL(), headers, nil)
	if err != nil {
		return RTSPResponse{}, err
	}
	c.captureSession(resp)
	return resp, nil
}

func (c *RTSPClient) Setup(clientRTPPort int) (RTSPResponse, error) {
	headers := map[string]string{
		"Transport": fmt.Sprintf("RTP/AVP;unicast;client_port=%d-%d", clientRTPPort, clientRTPPort+1),
	}
	resp, err := c.do("SETUP", c.cfg.TrackURL(1), headers, nil)
	if err != nil {
		return RTSPResponse{}, err
	}
	c.captureSession(resp)
	return resp, nil
}

func (c *RTSPClient) Play() (RTSPResponse, error) {
	headers := map[string]string{"Range": "npt=0.000-"}
	resp, err := c.do("PLAY", c.cfg.URL()+"/", c.withSession(headers), nil)
	if err != nil {
		return RTSPResponse{}, err
	}
	c.captureSession(resp)
	return resp, nil
}

func (c *RTSPClient) GetParameter() (RTSPResponse, error) {
	return c.do("GET_PARAMETER", c.cfg.URL(), c.withSession(nil), nil)
}

func (c *RTSPClient) Teardown() (RTSPResponse, error) {
	return c.do("TEARDOWN", c.cfg.URL()+"/", c.withSession(nil), nil)
}

func (c *RTSPClient) withSession(headers map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range headers {
		out[k] = v
	}
	if c.sessionID != "" {
		out["Session"] = c.sessionID
	}
	return out
}

func (c *RTSPClient) captureSession(resp RTSPResponse) {
	if s, ok := resp.Headers["Session"]; ok {
		c.sessionRaw = s
		if idx := strings.Index(s, ";"); idx > 0 {
			c.sessionID = s[:idx]
		} else {
			c.sessionID = s
		}
	}
}

func (c *RTSPClient) do(method, uri string, headers map[string]string, body []byte) (RTSPResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return RTSPResponse{}, errors.New("rtsp client closed")
	}
	if err := c.conn.SetDeadline(time.Now().Add(c.cfg.Timeout)); err != nil {
		return RTSPResponse{}, err
	}

	if headers == nil {
		headers = map[string]string{}
	}
	headers["CSeq"] = strconv.Itoa(c.cseq)
	c.cseq++
	if len(body) > 0 {
		headers["Content-Length"] = strconv.Itoa(len(body))
	}

	if _, err := fmt.Fprintf(c.writer, "%s %s RTSP/1.0\r\n", method, uri); err != nil {
		return RTSPResponse{}, err
	}
	for k, v := range headers {
		if _, err := fmt.Fprintf(c.writer, "%s: %s\r\n", k, v); err != nil {
			return RTSPResponse{}, err
		}
	}
	if _, err := c.writer.WriteString("\r\n"); err != nil {
		return RTSPResponse{}, err
	}
	if len(body) > 0 {
		if _, err := c.writer.Write(body); err != nil {
			return RTSPResponse{}, err
		}
	}
	if err := c.writer.Flush(); err != nil {
		return RTSPResponse{}, err
	}

	statusLine, err := c.reader.ReadString('\n')
	if err != nil {
		return RTSPResponse{}, err
	}
	statusLine = strings.TrimSpace(statusLine)
	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 3 {
		return RTSPResponse{}, fmt.Errorf("invalid rtsp status line: %s", statusLine)
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return RTSPResponse{}, err
	}

	respHeaders := map[string]string{}
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return RTSPResponse{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		k := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(line[:idx]))
		v := strings.TrimSpace(line[idx+1:])
		respHeaders[k] = v
	}

	var payload []byte
	if lengthText, ok := respHeaders["Content-Length"]; ok {
		length := parseIntDefault(lengthText, 0)
		if length > 0 {
			payload = make([]byte, length)
			if _, err := io.ReadFull(c.reader, payload); err != nil {
				return RTSPResponse{}, err
			}
		}
	}

	resp := RTSPResponse{StatusCode: code, Status: parts[2], Headers: respHeaders, Body: payload}
	if code < 200 || code >= 300 {
		return resp, fmt.Errorf("rtsp %s failed with %d %s", method, code, resp.Status)
	}
	return resp, nil
}
