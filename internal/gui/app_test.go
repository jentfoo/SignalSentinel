//go:build !headless

package gui

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	t.Parallel()

	base := Dependencies{
		SubscribeState: func(context.Context) <-chan RuntimeState {
			ch := make(chan RuntimeState, 1)
			ch <- RuntimeState{}
			close(ch)
			return ch
		},
		EnqueueControl: func(ControlIntent) {},
		StartRecording: func() error {
			return nil
		},
		StopRecording: func() error {
			return nil
		},
		LoadRecordings: func() ([]Recording, error) {
			return nil, nil
		},
		DeleteRecordings: func([]string) (DeleteReport, error) {
			return DeleteReport{}, nil
		},
		SaveSettings: func(Settings) error {
			return nil
		},
	}

	tests := []struct {
		name    string
		mutate  func(*Dependencies)
		wantErr string
	}{
		{
			name: "requires_state_subscription",
			mutate: func(deps *Dependencies) {
				deps.SubscribeState = nil
			},
			wantErr: "gui subscribe callback is required",
		},
		{
			name: "requires_control_callback",
			mutate: func(deps *Dependencies) {
				deps.EnqueueControl = nil
			},
			wantErr: "gui control callback is required",
		},
		{
			name: "requires_recording_start_callback",
			mutate: func(deps *Dependencies) {
				deps.StartRecording = nil
			},
			wantErr: "gui recording start callback is required",
		},
		{
			name: "requires_recording_stop_callback",
			mutate: func(deps *Dependencies) {
				deps.StopRecording = nil
			},
			wantErr: "gui recording stop callback is required",
		},
		{
			name: "requires_recordings_loader",
			mutate: func(deps *Dependencies) {
				deps.LoadRecordings = nil
			},
			wantErr: "gui recordings callback is required",
		},
		{
			name: "requires_recordings_delete_callback",
			mutate: func(deps *Dependencies) {
				deps.DeleteRecordings = nil
			},
			wantErr: "gui recordings delete callback is required",
		},
		{
			name: "requires_settings_saver",
			mutate: func(deps *Dependencies) {
				deps.SaveSettings = nil
			},
			wantErr: "gui save settings callback is required",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			deps := base
			tt.mutate(&deps)

			err := Run(t.Context(), deps)
			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
		})
	}
}
