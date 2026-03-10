//go:build !headless

package gui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQKStateToEnabledIndexes(t *testing.T) {
	t.Parallel()

	t.Run("extracts_enabled_only", func(t *testing.T) {
		values := []int{2, 1, 0, 2, 0, 1, 2}
		assert.Equal(t, []int{0, 3, 6}, qkStateToEnabledIndexes(values))
	})

	t.Run("empty_when_none_enabled", func(t *testing.T) {
		values := []int{0, 1, 0, 1, 0}
		assert.Empty(t, qkStateToEnabledIndexes(values))
	})

	t.Run("empty_input", func(t *testing.T) {
		assert.Empty(t, qkStateToEnabledIndexes(nil))
	})
}

func TestIndexesToQKState(t *testing.T) {
	t.Parallel()

	t.Run("sets_enabled_value", func(t *testing.T) {
		indexes := []int{0, 3, 6}
		state := indexesToQKState(indexes, 7)
		assert.Equal(t, []int{2, 0, 0, 2, 0, 0, 2}, state)
	})

	t.Run("ignores_out_of_range", func(t *testing.T) {
		indexes := []int{0, 5}
		state := indexesToQKState(indexes, 3)
		assert.Equal(t, []int{2, 0, 0}, state)
	})

	t.Run("empty_indexes", func(t *testing.T) {
		state := indexesToQKState(nil, 3)
		assert.Equal(t, []int{0, 0, 0}, state)
	})
}
