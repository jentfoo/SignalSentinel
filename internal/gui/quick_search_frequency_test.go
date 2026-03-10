package gui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseQuickSearchFrequency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		want    int
		wantErr string
	}{
		{name: "scanner_units_integer", value: "4601250", want: 4601250},
		{name: "hz_integer_value", value: "460125000", want: 4601250},
		{name: "mhz_decimal_value", value: "460.1250", want: 4601250},
		{name: "mhz_suffix_value", value: "460.125 mhz", want: 4601250},
		{name: "hz_suffix_value", value: "460125000hz", want: 4601250},
		{name: "commas_supported_input", value: "460,125,000", want: 4601250},
		{name: "empty_value_error", value: " ", wantErr: "quick search frequency is required"},
		{name: "invalid_text_error", value: "abc", wantErr: "quick search frequency must be numeric"},
		{name: "zero_value_error", value: "0", wantErr: "quick search frequency must be > 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseQuickSearchFrequency(tt.value)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
