package recording

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mewkiz/flac"
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

		fi, err := os.Stat(w.tmpPath)
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

		f, err := os.Open(out)
		require.NoError(t, err)
		t.Cleanup(func() { _ = f.Close() })

		stream, err := flac.New(f)
		require.NoError(t, err)

		nFrames := 0
		for {
			_, err := stream.ParseNext()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			nFrames++
		}
		assert.Greater(t, nFrames, 0)
	})

	t.Run("removes_temp_file", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "clip.flac")
		w, err := NewFLACWriter(out)
		require.NoError(t, err)
		tmpPath := w.tmpPath

		require.NoError(t, w.WritePCM([]int16{1, 2, 3}))
		_, err = w.Finalize()
		require.NoError(t, err)

		_, err = os.Stat(tmpPath)
		require.Error(t, err)
		assert.ErrorIs(t, err, os.ErrNotExist)

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
	f, err := os.Open(out)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	stream, err := flac.New(f)
	require.NoError(t, err)

	var gotBlockSizes []uint16
	for {
		fr, err := stream.ParseNext()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		gotBlockSizes = append(gotBlockSizes, fr.BlockSize)
	}
	assert.Equal(t, []uint16{1024, 1024, 452}, gotBlockSizes)
}

func TestFLACWriterAbort(t *testing.T) {
	t.Parallel()

	out := filepath.Join(t.TempDir(), "clip.flac")
	w, err := NewFLACWriter(out)
	require.NoError(t, err)
	require.NotEmpty(t, w.tmpPath)

	tmpPath := w.tmpPath
	require.NoError(t, w.Abort())
	_, err = os.Stat(tmpPath)
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
