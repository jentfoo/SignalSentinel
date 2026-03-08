package recording

import (
	"crypto/md5"
	"encoding/binary"
	"errors"
	"hash"
	"os"
	"path/filepath"

	"github.com/google/renameio/v2"
)

const (
	flacSampleRate    = 8000
	flacChannels      = 1
	flacBitsPerSample = 16
	defaultBlockSize  = 1024
)

// PCMWriter writes 16-bit mono PCM and atomically finalizes a FLAC file.
type PCMWriter interface {
	WritePCM(samples []int16) error
	Finalize() (int64, error)
	Abort() error
}

// FLACWriter writes FLAC files using verbatim subframes.
type FLACWriter struct {
	finalPath string
	f         *renameio.PendingFile

	pending []int16

	totalSamples uint64
	frameNumber  uint64
	minFrameSize int
	maxFrameSize int
	minBlockSize int
	maxBlockSize int
	closed       bool

	md5 hash.Hash
}

// NewFLACWriter creates a new atomic FLAC writer.
func NewFLACWriter(finalPath string) (*FLACWriter, error) {
	if finalPath == "" {
		return nil, errors.New("final path is required")
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return nil, err
	}
	tmp, err := renameio.TempFile(filepath.Dir(finalPath), finalPath)
	if err != nil {
		return nil, err
	}

	w := &FLACWriter{
		finalPath: finalPath,
		f:         tmp,
		md5:       md5.New(),
	}
	if err := w.writeHeader(); err != nil {
		_ = tmp.Cleanup()
		return nil, err
	}
	return w, nil
}

func (w *FLACWriter) writeHeader() error {
	if _, err := w.f.Write([]byte("fLaC")); err != nil {
		return err
	}
	// Last metadata block + STREAMINFO type + 34-byte payload
	if _, err := w.f.Write([]byte{0x80, 0x00, 0x00, 0x22}); err != nil {
		return err
	}
	// Placeholder STREAMINFO, patched on finalize
	payload := make([]byte, 34)
	binary.BigEndian.PutUint16(payload[0:2], defaultBlockSize)
	binary.BigEndian.PutUint16(payload[2:4], defaultBlockSize)
	if _, err := w.f.Write(payload); err != nil {
		return err
	}
	return nil
}

// WritePCM writes PCM samples to FLAC frames.
func (w *FLACWriter) WritePCM(samples []int16) error {
	if len(samples) == 0 {
		return nil
	}
	if w.closed {
		return errors.New("writer closed")
	}
	for _, s := range samples {
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], uint16(s))
		_, _ = w.md5.Write(b[:])
	}

	w.pending = append(w.pending, samples...)
	for len(w.pending) >= defaultBlockSize {
		if err := w.writeFrame(w.pending[:defaultBlockSize]); err != nil {
			return err
		}
		w.pending = w.pending[defaultBlockSize:]
	}
	return nil
}

func (w *FLACWriter) writeFrame(samples []int16) error {
	if len(samples) == 0 {
		return nil
	}
	frame := make([]byte, 0, 64+len(samples)*2)

	blockSizeCode, blockSizeExtra, err := flacBlockSizeCode(len(samples))
	if err != nil {
		return err
	}
	// Header: sync + fixed-block strategy + block size code + sample rate code + mono + 16-bit samples
	header := []byte{0xFF, 0xF8, (blockSizeCode << 4) | 0x0C, 0x08}
	frame = append(frame, header...)
	encodedFrameNumber, err := encodeUTF8Uint64(w.frameNumber)
	if err != nil {
		return err
	}
	frame = append(frame, encodedFrameNumber...)
	frame = append(frame, blockSizeExtra...)
	frame = append(frame, byte(flacSampleRate/1000))
	frame = append(frame, crc8(frame))

	// Subframe: zero wasted bits + verbatim coding
	frame = append(frame, 0x02)
	for _, sample := range samples {
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(sample))
		frame = append(frame, b[0], b[1])
	}

	crc := crc16(frame)
	frame = append(frame, byte(crc>>8), byte(crc))

	if _, err := w.f.Write(frame); err != nil {
		return err
	}

	frameSize := len(frame)
	if w.minFrameSize == 0 || frameSize < w.minFrameSize {
		w.minFrameSize = frameSize
	}
	if frameSize > w.maxFrameSize {
		w.maxFrameSize = frameSize
	}
	blockSize := len(samples)
	if w.minBlockSize == 0 || blockSize < w.minBlockSize {
		w.minBlockSize = blockSize
	}
	if blockSize > w.maxBlockSize {
		w.maxBlockSize = blockSize
	}
	w.totalSamples += uint64(len(samples))
	w.frameNumber++
	return nil
}

func flacBlockSizeCode(blockSize int) (byte, []byte, error) {
	if blockSize <= 0 {
		return 0, nil, errors.New("invalid block size")
	}
	if blockSize == defaultBlockSize {
		return 0xA, nil, nil
	}
	if blockSize <= 256 {
		return 0x6, []byte{byte(blockSize - 1)}, nil
	}
	if blockSize <= 65536 {
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(blockSize-1))
		return 0x7, b[:], nil
	}
	return 0, nil, errors.New("unsupported block size")
}

// Finalize flushes and atomically moves the file into place.
func (w *FLACWriter) Finalize() (int64, error) {
	if w.closed {
		fi, err := os.Stat(w.finalPath)
		if err != nil {
			return 0, err
		}
		return fi.Size(), nil
	}
	if len(w.pending) > 0 {
		if err := w.writeFrame(w.pending); err != nil {
			return 0, err
		}
		w.pending = nil
	}
	if err := w.patchStreamInfo(); err != nil {
		return 0, err
	}
	if err := w.f.CloseAtomicallyReplace(); err != nil {
		return 0, err
	}
	w.closed = true
	w.f = nil
	fi, err := os.Stat(w.finalPath)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// Abort discards any temporary output.
func (w *FLACWriter) Abort() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if w.f != nil {
		_ = w.f.Cleanup()
		w.f = nil
	}
	return nil
}

func (w *FLACWriter) patchStreamInfo() error {
	payload := make([]byte, 34)
	minBS := uint16(w.minBlockSize)
	maxBS := uint16(w.maxBlockSize)
	if minBS == 0 {
		minBS = defaultBlockSize
	}
	if maxBS == 0 {
		maxBS = defaultBlockSize
	}
	binary.BigEndian.PutUint16(payload[0:2], minBS)
	binary.BigEndian.PutUint16(payload[2:4], maxBS)
	putUint24(payload[4:7], uint32(w.minFrameSize))
	putUint24(payload[7:10], uint32(w.maxFrameSize))

	x := uint64(flacSampleRate&0xFFFFF)<<44 |
		uint64((flacChannels-1)&0x7)<<41 |
		uint64((flacBitsPerSample-1)&0x1F)<<36 |
		(w.totalSamples & 0xFFFFFFFFF)
	for i := 0; i < 8; i++ {
		payload[10+i] = byte(x >> (56 - 8*i))
	}
	copy(payload[18:], w.md5.Sum(nil))

	if _, err := w.f.Seek(8, 0); err != nil {
		return err
	}
	if _, err := w.f.Write(payload); err != nil {
		return err
	}
	return nil
}

func putUint24(dst []byte, v uint32) {
	dst[0] = byte(v >> 16)
	dst[1] = byte(v >> 8)
	dst[2] = byte(v)
}

func encodeUTF8Uint64(v uint64) ([]byte, error) {
	switch {
	case v <= 0x7F:
		return []byte{byte(v)}, nil
	case v <= 0x7FF:
		return []byte{byte(0xC0 | (v >> 6)), byte(0x80 | (v & 0x3F))}, nil
	case v <= 0xFFFF:
		return []byte{byte(0xE0 | (v >> 12)), byte(0x80 | ((v >> 6) & 0x3F)), byte(0x80 | (v & 0x3F))}, nil
	case v <= 0x1FFFFF:
		return []byte{
			byte(0xF0 | (v >> 18)),
			byte(0x80 | ((v >> 12) & 0x3F)),
			byte(0x80 | ((v >> 6) & 0x3F)),
			byte(0x80 | (v & 0x3F)),
		}, nil
	case v <= 0x3FFFFFF:
		return []byte{
			byte(0xF8 | (v >> 24)),
			byte(0x80 | ((v >> 18) & 0x3F)),
			byte(0x80 | ((v >> 12) & 0x3F)),
			byte(0x80 | ((v >> 6) & 0x3F)),
			byte(0x80 | (v & 0x3F)),
		}, nil
	case v <= 0x7FFFFFFF:
		return []byte{
			byte(0xFC | (v >> 30)),
			byte(0x80 | ((v >> 24) & 0x3F)),
			byte(0x80 | ((v >> 18) & 0x3F)),
			byte(0x80 | ((v >> 12) & 0x3F)),
			byte(0x80 | ((v >> 6) & 0x3F)),
			byte(0x80 | (v & 0x3F)),
		}, nil
	case v <= 0xFFFFFFFFF:
		return []byte{
			0xFE,
			byte(0x80 | ((v >> 30) & 0x3F)),
			byte(0x80 | ((v >> 24) & 0x3F)),
			byte(0x80 | ((v >> 18) & 0x3F)),
			byte(0x80 | ((v >> 12) & 0x3F)),
			byte(0x80 | ((v >> 6) & 0x3F)),
			byte(0x80 | (v & 0x3F)),
		}, nil
	default:
		return nil, errors.New("value exceeds FLAC UTF-8 integer range")
	}
}

func crc8(data []byte) byte {
	var crc byte
	for _, b := range data {
		crc ^= b
		for i := 0; i < 8; i++ {
			if crc&0x80 != 0 {
				crc = (crc << 1) ^ 0x07
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func crc16(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
