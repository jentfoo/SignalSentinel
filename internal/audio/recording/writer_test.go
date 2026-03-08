package recording

import (
	"crypto/md5"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFLACWriter(t *testing.T) {
	t.Parallel()

	t.Run("rejects_empty_path", func(t *testing.T) {
		w, err := NewFLACWriter("")
		require.Error(t, err)
		assert.Nil(t, w)
		assert.Equal(t, "final path is required", err.Error())
	})

	t.Run("creates_writer_file", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "clip.flac")
		w, err := NewFLACWriter(out)
		require.NoError(t, err)
		require.NotNil(t, w)
		require.NotNil(t, w.f)
		assert.Equal(t, filepath.Dir(out), filepath.Dir(w.f.Name()))
		require.NoError(t, w.Abort())
	})
}

func TestFLACWriterWritePCM(t *testing.T) {
	t.Parallel()

	t.Run("writes_after_create", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "clip.flac")
		w, err := NewFLACWriter(out)
		require.NoError(t, err)

		require.NoError(t, w.WritePCM([]int16{1, 2, 3, 4}))
		_, err = w.Finalize()
		require.NoError(t, err)
		require.Error(t, w.WritePCM([]int16{5, 6}))
	})

	t.Run("empty_samples", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "clip.flac")
		w, err := NewFLACWriter(out)
		require.NoError(t, err)
		defer func() { _ = w.Abort() }()

		require.NoError(t, w.WritePCM(nil))
		require.NoError(t, w.WritePCM([]int16{}))
	})

	t.Run("exact_block_size", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "clip.flac")
		w, err := NewFLACWriter(out)
		require.NoError(t, err)

		samples := make([]int16, defaultBlockSize)
		for i := range samples {
			samples[i] = int16(i % 100)
		}
		require.NoError(t, w.WritePCM(samples))

		tmpPath := pendingPathFromWriter(t, w)
		fi, err := os.Stat(tmpPath)
		require.NoError(t, err)
		assert.Greater(t, fi.Size(), int64(42))

		_, err = w.Finalize()
		require.NoError(t, err)
	})
}

func TestFLACWriterFinalize(t *testing.T) {
	t.Parallel()

	t.Run("writes_valid_flac", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "clip.flac")
		w, err := NewFLACWriter(out)
		require.NoError(t, err)

		samples := make([]int16, 5000)
		for i := range samples {
			samples[i] = int16((i % 400) - 200)
		}
		require.NoError(t, w.WritePCM(samples))
		size, err := w.Finalize()
		require.NoError(t, err)
		assert.Greater(t, size, int64(42))

		b, err := os.ReadFile(out)
		require.NoError(t, err)
		require.Greater(t, len(b), 4)
		assert.Equal(t, []byte{'f', 'L', 'a', 'C'}, b[:4])
	})

	t.Run("validates_streaminfo_fields", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "clip.flac")
		w, err := NewFLACWriter(out)
		require.NoError(t, err)

		samples := make([]int16, 2048)
		for i := range samples {
			samples[i] = int16((i % 300) - 150)
		}
		require.NoError(t, w.WritePCM(samples))
		_, err = w.Finalize()
		require.NoError(t, err)

		b, err := os.ReadFile(out)
		require.NoError(t, err)

		// Bytes 0-3: fLaC magic
		assert.Equal(t, []byte("fLaC"), b[:4])

		// Byte 4: metadata block header (last-block=1, type=0 STREAMINFO)
		assert.Equal(t, byte(0x80), b[4]&0x80)
		assert.Equal(t, byte(0x00), b[4]&0x7F)

		// STREAMINFO starts at offset 8
		si := b[8:42]

		// Min/max block size = 1024
		minBS := binary.BigEndian.Uint16(si[0:2])
		maxBS := binary.BigEndian.Uint16(si[2:4])
		assert.Equal(t, uint16(1024), minBS)
		assert.Equal(t, uint16(1024), maxBS)

		// Sample rate (20 bits at byte offset 10): verify 8000
		sampleRate := (uint32(si[10]) << 12) | (uint32(si[11]) << 4) | (uint32(si[12]) >> 4)
		assert.Equal(t, uint32(8000), sampleRate)

		// Channels (3 bits): verify 0 (= 1 channel)
		channels := (si[12] >> 1) & 0x07
		assert.Equal(t, byte(0), channels)

		// Bits per sample (5 bits): verify 15 (= 16 bits)
		bps := ((si[12] & 0x01) << 4) | (si[13] >> 4)
		assert.Equal(t, byte(15), bps)

		// Total samples (36 bits): verify matches written count
		totalSamples := (uint64(si[13]&0x0F) << 32) | uint64(binary.BigEndian.Uint32(si[14:18]))
		assert.Equal(t, uint64(2048), totalSamples)

		// MD5: recompute over original samples in little-endian signed 16-bit
		h := md5.New()
		for _, s := range samples {
			var buf [2]byte
			binary.LittleEndian.PutUint16(buf[:], uint16(s))
			h.Write(buf[:])
		}
		expectedMD5 := h.Sum(nil)
		assert.Equal(t, expectedMD5, si[18:34])
	})

	t.Run("removes_temp_file", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "clip.flac")
		w, err := NewFLACWriter(out)
		require.NoError(t, err)
		tmpPath := pendingPathFromWriter(t, w)

		require.NoError(t, w.WritePCM([]int16{1, 2, 3}))
		_, err = w.Finalize()
		require.NoError(t, err)

		_, err = os.Stat(tmpPath)
		require.Error(t, err)
		require.ErrorIs(t, err, os.ErrNotExist)

		// Verify final file exists
		_, err = os.Stat(out)
		require.NoError(t, err)
	})
}

func TestFLACWriterPartialFinalFrameBlockSize(t *testing.T) {
	t.Parallel()

	out := filepath.Join(t.TempDir(), "clip.flac")
	w, err := NewFLACWriter(out)
	require.NoError(t, err)
	samples := make([]int16, 2500)
	for i := range samples {
		samples[i] = int16((i % 300) - 150)
	}
	require.NoError(t, w.WritePCM(samples))
	_, err = w.Finalize()
	require.NoError(t, err)
	b, err := os.ReadFile(out)
	require.NoError(t, err)
	si := b[8:42]
	assert.Equal(t, uint16(452), binary.BigEndian.Uint16(si[0:2]))
	assert.Equal(t, uint16(1024), binary.BigEndian.Uint16(si[2:4]))

	// First frame should advertise 16-bit samples.
	firstFrame := b[42:]
	require.GreaterOrEqual(t, len(firstFrame), 4)
	assert.Equal(t, byte(0xAC), firstFrame[2])
	assert.Equal(t, byte(0x08), firstFrame[3])

	// Two full 1024-sample frames precede the final 452-sample frame.
	const fullFrameLen = 2058
	finalFrameOffset := 42 + (2 * fullFrameLen)
	require.GreaterOrEqual(t, len(b), finalFrameOffset+4)
	finalFrame := b[finalFrameOffset:]
	assert.Equal(t, byte(0x7C), finalFrame[2])
	assert.Equal(t, byte(0x08), finalFrame[3])
	assert.Equal(t, uint16(451), binary.BigEndian.Uint16(finalFrame[5:7]))
}

func TestFLACWriterAbort(t *testing.T) {
	t.Parallel()

	out := filepath.Join(t.TempDir(), "clip.flac")
	w, err := NewFLACWriter(out)
	require.NoError(t, err)
	tmpPath := pendingPathFromWriter(t, w)

	require.NoError(t, w.Abort())
	_, err = os.Stat(tmpPath)
	require.Error(t, err)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestEncodeUTF8Uint64(t *testing.T) {
	t.Parallel()

	t.Run("supports_flac_extended_lengths", func(t *testing.T) {
		encoded, err := encodeUTF8Uint64(0x3FFFFFF)
		require.NoError(t, err)
		assert.Len(t, encoded, 5)

		encoded, err = encodeUTF8Uint64(0x7FFFFFFF)
		require.NoError(t, err)
		assert.Len(t, encoded, 6)

		encoded, err = encodeUTF8Uint64(0xFFFFFFFFF)
		require.NoError(t, err)
		assert.Len(t, encoded, 7)
		assert.Equal(t, byte(0xFE), encoded[0])
	})

	t.Run("rejects_values_out_of_range", func(t *testing.T) {
		encoded, err := encodeUTF8Uint64(0x1000000000)
		require.Error(t, err)
		assert.Nil(t, encoded)
		assert.Equal(t, "value exceeds FLAC UTF-8 integer range", err.Error())
	})
}

func pendingPathFromWriter(t *testing.T, w *FLACWriter) string {
	t.Helper()

	require.NotNil(t, w)
	require.NotNil(t, w.f)
	if w.f.Name() == "" {
		t.Fatalf("pending output file path is empty")
	}
	return w.f.Name()
}
