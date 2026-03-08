package ingest

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/jentfoo/SignalSentinel/internal/sds200"
)

const (
	frameBufferSize        = 256
	healthySessionDuration = 30 * time.Second
)

// Frame is one decoded PCM frame from the RTP stream.
type Frame struct {
	Samples      []int16
	ReceivedAt   time.Time
	RTPTimestamp uint32
}

// Config defines RTSP/RTP ingest behavior.
type Config struct {
	Address           string
	RTSPPort          int
	RTSPPath          string
	RTPReadTimeout    time.Duration
	ReconnectDelay    time.Duration
	MaxReconnectFails int
	Logger            *log.Logger
	OnFrame           func(Frame)
}

type Session struct {
	cfg Config

	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	fatal       chan error
	frames      chan Frame
	connectedAt time.Time
}

func NewSession(parent context.Context, cfg Config) (*Session, error) {
	if cfg.Address == "" {
		return nil, errors.New("rtsp address is required")
	}
	if cfg.RTSPPort == 0 {
		cfg.RTSPPort = sds200.DefaultRTSPPort
	}
	if cfg.RTPReadTimeout == 0 {
		cfg.RTPReadTimeout = 2000 * time.Millisecond
	}
	if cfg.ReconnectDelay == 0 {
		cfg.ReconnectDelay = 2 * time.Second
	}
	if cfg.MaxReconnectFails <= 0 {
		cfg.MaxReconnectFails = 5
	}
	ctx, cancel := context.WithCancel(parent)
	s := &Session{cfg: cfg, ctx: ctx, cancel: cancel, fatal: make(chan error, 1), frames: make(chan Frame, frameBufferSize)}
	s.wg.Add(2)
	go s.loop()
	go s.drainFrames()
	return s, nil
}

func (s *Session) Fatal() <-chan error { return s.fatal }

func (s *Session) Close() error {
	s.cancel()
	s.wg.Wait()
	return nil
}

func (s *Session) drainFrames() {
	defer s.wg.Done()
	for frame := range s.frames {
		if s.cfg.OnFrame != nil {
			s.cfg.OnFrame(frame)
		}
	}
}

func (s *Session) loop() {
	defer s.wg.Done()
	defer close(s.frames)
	var fails int
	for {
		if s.ctx.Err() != nil {
			return
		}
		err := s.runOnce()
		if err == nil || s.ctx.Err() != nil {
			fails = 0
			continue
		}
		if !s.connectedAt.IsZero() && time.Since(s.connectedAt) >= healthySessionDuration {
			fails = 0
		}
		fails++
		s.logf("audio ingest reconnect attempt %d/%d after error: %v", fails, s.cfg.MaxReconnectFails, err)
		if fails >= s.cfg.MaxReconnectFails {
			select {
			case s.fatal <- fmt.Errorf("audio ingest reconnect budget exceeded: %w", err):
			default:
			}
			return
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(s.cfg.ReconnectDelay):
		}
	}
}

func (s *Session) runOnce() error {
	s.connectedAt = time.Time{}
	addr, err := net.ResolveUDPAddr("udp", ":0")
	if err != nil {
		return err
	}
	rtpConn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = rtpConn.Close() }()

	localPort := rtpConn.LocalAddr().(*net.UDPAddr).Port
	rtspClient, err := sds200.NewRTSPClient(sds200.RTSPConfig{Address: s.cfg.Address, Port: s.cfg.RTSPPort, Path: s.cfg.RTSPPath})
	if err != nil {
		return err
	}
	defer func() {
		_, _ = rtspClient.Teardown()
		_ = rtspClient.Close()
	}()
	if _, err := rtspClient.Options(); err != nil {
		return err
	} else if _, err := rtspClient.Describe(); err != nil {
		return err
	} else if _, err := rtspClient.Setup(localPort); err != nil {
		return err
	} else if _, err := rtspClient.Play(); err != nil {
		return err
	}
	s.connectedAt = time.Now()

	keepAliveTicker := time.NewTicker(30 * time.Second)
	defer keepAliveTicker.Stop()

	expectedIP := net.ParseIP(s.cfg.Address)
	buf := make([]byte, 2000)
	for {
		select {
		case <-s.ctx.Done():
			return nil
		case <-keepAliveTicker.C:
			if _, err := rtspClient.GetParameter(); err != nil {
				return err
			}
		default:
		}
		if err := rtpConn.SetReadDeadline(time.Now().Add(s.cfg.RTPReadTimeout)); err != nil {
			return err
		}
		n, remoteAddr, err := rtpConn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		if expectedIP != nil && !remoteAddr.IP.Equal(expectedIP) {
			continue
		}
		payload, pErr := parseRTPPayload(buf[:n])
		if pErr != nil {
			continue
		}
		rtpTS := extractRTPTimestamp(buf[:n])
		samples := decodeULaw(payload)
		if len(samples) == 0 {
			continue
		}
		frame := Frame{Samples: samples, ReceivedAt: time.Now(), RTPTimestamp: rtpTS}
		select {
		case s.frames <- frame:
		default:
			select {
			case <-s.frames:
			default:
			}
			select {
			case s.frames <- frame:
			default:
			}
		}
	}
}

func extractRTPTimestamp(pkt []byte) uint32 {
	if len(pkt) < 8 {
		return 0
	}
	return binary.BigEndian.Uint32(pkt[4:8])
}

func parseRTPPayload(pkt []byte) ([]byte, error) {
	if len(pkt) < 12 {
		return nil, errors.New("short rtp packet")
	}
	v := pkt[0] >> 6
	if v != 2 {
		return nil, errors.New("unsupported rtp version")
	}
	hasPadding := (pkt[0] & 0x20) != 0
	cc := int(pkt[0] & 0x0F)
	hasExt := (pkt[0] & 0x10) != 0
	pt := pkt[1] & 0x7F
	if pt != 0 {
		return nil, fmt.Errorf("unsupported payload type: %d", pt)
	}
	offset := 12 + cc*4
	if len(pkt) < offset {
		return nil, errors.New("invalid csrc count")
	}
	if hasExt {
		if len(pkt) < offset+4 {
			return nil, errors.New("invalid extension header")
		}
		extLenWords := int(binary.BigEndian.Uint16(pkt[offset+2 : offset+4]))
		offset += 4 + extLenWords*4
		if len(pkt) < offset {
			return nil, errors.New("invalid extension length")
		}
	}
	if offset >= len(pkt) {
		return nil, errors.New("empty payload")
	}
	end := len(pkt)
	if hasPadding {
		padLen := int(pkt[len(pkt)-1])
		if padLen == 0 {
			return nil, errors.New("invalid padding length")
		} else if padLen > len(pkt)-offset {
			return nil, errors.New("padding exceeds payload length")
		}
		end -= padLen
	}
	if offset >= end {
		return nil, errors.New("empty payload")
	}
	return pkt[offset:end], nil
}

func decodeULaw(payload []byte) []int16 {
	out := make([]int16, len(payload))
	for i, b := range payload {
		out[i] = ulawToPCM16(b)
	}
	return out
}

func ulawToPCM16(u byte) int16 {
	u = ^u
	sign := u & 0x80
	exponent := (u >> 4) & 0x07
	mantissa := u & 0x0F
	sample := ((int(mantissa) << 3) + 0x84) << exponent
	sample -= 0x84
	if sign != 0 {
		return int16(-sample)
	}
	return int16(sample)
}

func (s *Session) logf(format string, args ...any) {
	if s.cfg.Logger != nil {
		s.cfg.Logger.Printf("ingest: "+format, args...)
	}
}
