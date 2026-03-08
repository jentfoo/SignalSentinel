//go:build headless

package gui

import (
	"context"
	"errors"
)

func Run(_ context.Context, deps Dependencies) error {
	_ = deps
	return errors.New("gui disabled in headless build")
}
