//go:build headless

package gui

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunHeadless(t *testing.T) {
	t.Parallel()

	t.Run("returns_error_when_active", func(t *testing.T) {
		err := Run(context.Background(), Dependencies{})
		require.Error(t, err)
		assert.Equal(t, "gui disabled in headless build", err.Error())
	})

	t.Run("returns_error_when_canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := Run(ctx, Dependencies{})
		require.Error(t, err)
		assert.Equal(t, "gui disabled in headless build", err.Error())
	})
}
