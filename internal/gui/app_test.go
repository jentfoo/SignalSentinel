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
		ExecuteControl:  func(ControlRequest) ControlResult { return ControlResult{} },
		LoadScannerList: func(string, int) ([]ListItem, error) { return nil, nil },
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
				deps.ExecuteControl = nil
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

func TestFormatListSelectOption(t *testing.T) {
	t.Parallel()

	t.Run("name_qk_and_index", func(t *testing.T) {
		item := ListItem{Tag: "FL", Attrs: map[string]string{"Index": "0", "Name": "Main List", "Q_Key": "1"}}
		assert.Equal(t, "Main List (QK 1, idx 0)", formatListSelectOption(item))
	})

	t.Run("name_and_index_no_qk", func(t *testing.T) {
		item := ListItem{Tag: "FL", Attrs: map[string]string{"Index": "0", "Name": "Main List", "Q_Key": "None"}}
		assert.Equal(t, "Main List (idx 0)", formatListSelectOption(item))
	})

	t.Run("name_and_qk_only", func(t *testing.T) {
		item := ListItem{Tag: "SYS", Attrs: map[string]string{"Name": "County Fire", "Q_Key": "5"}}
		assert.Equal(t, "County Fire (QK 5)", formatListSelectOption(item))
	})

	t.Run("name_only", func(t *testing.T) {
		item := ListItem{Tag: "FL", Attrs: map[string]string{"Name": "Main List"}}
		assert.Equal(t, "Main List", formatListSelectOption(item))
	})

	t.Run("index_only_uses_tag", func(t *testing.T) {
		item := ListItem{Tag: "SYS", Attrs: map[string]string{"Index": "5"}}
		assert.Equal(t, "SYS (idx 5)", formatListSelectOption(item))
	})

	t.Run("no_attrs_uses_tag", func(t *testing.T) {
		item := ListItem{Tag: "DEPT", Attrs: map[string]string{}}
		assert.Equal(t, "DEPT", formatListSelectOption(item))
	})
}

func TestFormatListItems(t *testing.T) {
	t.Parallel()

	t.Run("index_and_name", func(t *testing.T) {
		items := []ListItem{
			{Tag: "FL", Attrs: map[string]string{"Index": "0", "Name": "Favorites 1", "Monitor": "On"}},
			{Tag: "FL", Attrs: map[string]string{"Index": "1", "Name": "Favorites 2", "Monitor": "Off"}},
		}
		result := formatListItems(items)
		assert.Contains(t, result, "[0] Favorites 1")
		assert.Contains(t, result, "[1] Favorites 2")
		assert.Contains(t, result, "Monitor=On")
		assert.Contains(t, result, "Monitor=Off")
	})

	t.Run("name_only", func(t *testing.T) {
		items := []ListItem{
			{Tag: "SYS", Attrs: map[string]string{"Name": "County Fire"}},
		}
		result := formatListItems(items)
		assert.Equal(t, "County Fire", result)
	})

	t.Run("index_only", func(t *testing.T) {
		items := []ListItem{
			{Tag: "SFREQ", Attrs: map[string]string{"Index": "5", "Freq": "155.2200"}},
		}
		result := formatListItems(items)
		assert.Contains(t, result, "[5] SFREQ")
		assert.Contains(t, result, "Freq=155.2200")
	})

	t.Run("empty_list", func(t *testing.T) {
		result := formatListItems(nil)
		assert.Empty(t, result)
	})

	t.Run("tag_only_fallback", func(t *testing.T) {
		items := []ListItem{
			{Tag: "AFREQ", Attrs: map[string]string{"Freq": "460.0000", "Avoid": "Off"}},
		}
		result := formatListItems(items)
		assert.Contains(t, result, "AFREQ")
		assert.Contains(t, result, "Avoid=Off")
		assert.Contains(t, result, "Freq=460.0000")
	})
}
