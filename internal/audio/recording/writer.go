package recording

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/google/renameio/v2"
	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
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
	tmpPath   string
	f         *renameio.PendingFile
	enc       *flac.Encoder

	pending []int16
	closed  bool
}

type pendingFileWriteSeeker struct {
	f *renameio.PendingFile
}

func (p *pendingFileWriteSeeker) Write(b []byte) (int, error) {
	return p.f.Write(b)
}

func (p *pendingFileWriteSeeker) Seek(offset int64, whence int) (int64, error) {
	return p.f.Seek(offset, whence)
}

// NewFLACWriter creates a new atomic FLAC writer.
func NewFLACWriter(finalPath string) (*FLACWriter, error) {
	if finalPath == "" {
		return nil, errors.New("final path is required")
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return nil, err
	}
	tmp, err := renameio.TempFile("", finalPath)
	if err != nil {
		return nil, err
	}

	info := &meta.StreamInfo{
		BlockSizeMin:  defaultBlockSize,
		BlockSizeMax:  defaultBlockSize,
		SampleRate:    flacSampleRate,
		NChannels:     flacChannels,
		BitsPerSample: flacBitsPerSample,
	}
	enc, err := flac.NewEncoder(&pendingFileWriteSeeker{f: tmp}, info)
	if err != nil {
		_ = tmp.Cleanup()
		return nil, err
	}
	enc.EnablePredictionAnalysis(false)

	return &FLACWriter{
		finalPath: finalPath,
		tmpPath:   tmp.Name(),
		f:         tmp,
		enc:       enc,
	}, nil
}

// WritePCM writes PCM samples to FLAC frames.
func (w *FLACWriter) WritePCM(samples []int16) error {
	if len(samples) == 0 {
		return nil
	}
	if w.closed {
		return errors.New("writer closed")
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

	pcm := make([]int32, len(samples))
	for i, sample := range samples {
		pcm[i] = int32(sample)
	}

	f := &frame.Frame{
		Header: frame.Header{
			HasFixedBlockSize: true,
			BlockSize:         uint16(len(samples)),
			SampleRate:        flacSampleRate,
			Channels:          frame.ChannelsMono,
			BitsPerSample:     flacBitsPerSample,
		},
		Subframes: []*frame.Subframe{{
			SubHeader: frame.SubHeader{Pred: frame.PredVerbatim},
			NSamples:  len(samples),
			Samples:   pcm,
		}},
	}
	return w.enc.WriteFrame(f)
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
	if err := w.enc.Close(); err != nil {
		return 0, err
	}
	w.enc = nil
	if err := w.f.CloseAtomicallyReplace(); err != nil {
		return 0, err
	}
	w.closed = true
	w.f = nil
	w.tmpPath = ""
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
	w.enc = nil
	if w.f != nil {
		_ = w.f.Cleanup()
		w.f = nil
	}
	return nil
}
