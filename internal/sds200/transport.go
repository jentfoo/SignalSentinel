package sds200

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

type TransportConfig struct {
	Address      string
	Port         int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	BufferSize   int
}

func (c TransportConfig) withDefaults() TransportConfig {
	if c.Port == 0 {
		c.Port = DefaultControlPort
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = 2 * time.Second
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = 2 * time.Second
	}
	if c.BufferSize == 0 {
		c.BufferSize = 64 * 1024
	}
	return c
}

type UDPTransport struct {
	cfg    TransportConfig
	conn   *net.UDPConn
	inbox  chan []byte
	errCh  chan error
	close  chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
	mu     sync.RWMutex
	closed bool
}

func NewUDPTransport(cfg TransportConfig) (*UDPTransport, error) {
	cfg = cfg.withDefaults()
	if cfg.Address == "" {
		return nil, errors.New("address is required")
	}

	raddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", cfg.Address, cfg.Port))
	if err != nil {
		return nil, fmt.Errorf("resolve udp address: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return nil, fmt.Errorf("dial udp: %w", err)
	}

	t := &UDPTransport{
		cfg:   cfg,
		conn:  conn,
		inbox: make(chan []byte, 512),
		errCh: make(chan error, 8),
		close: make(chan struct{}),
	}

	t.wg.Add(1)
	go t.readLoop()
	return t, nil
}

func (t *UDPTransport) readLoop() {
	defer t.wg.Done()
	buf := make([]byte, t.cfg.BufferSize)
	for {
		select {
		case <-t.close:
			return
		default:
		}

		_ = t.conn.SetReadDeadline(time.Now().Add(t.cfg.ReadTimeout))
		n, err := t.conn.Read(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			select {
			case t.errCh <- err:
			default:
			}
			return
		}
		payload := make([]byte, n)
		copy(payload, buf[:n])
		select {
		case t.inbox <- payload:
		case <-t.close:
			return
		}
	}
}

func (t *UDPTransport) Send(ctx context.Context, frame []byte) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.closed {
		return errors.New("transport closed")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = t.conn.SetWriteDeadline(deadline)
	} else {
		_ = t.conn.SetWriteDeadline(time.Now().Add(t.cfg.WriteTimeout))
	}
	_, err := t.conn.Write(frame)
	if err != nil {
		return fmt.Errorf("send command: %w", err)
	}
	return nil
}

func (t *UDPTransport) Receive(ctx context.Context) ([]byte, error) {
	select {
	case msg := <-t.inbox:
		return msg, nil
	case err := <-t.errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.close:
		return nil, errors.New("transport closed")
	}
}

func (t *UDPTransport) Close() error {
	var err error
	t.once.Do(func() {
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		close(t.close)
		err = t.conn.Close()
		t.wg.Wait()
	})
	return err
}
