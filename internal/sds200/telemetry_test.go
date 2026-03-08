package sds200

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSTS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fields  []string
		wantErr bool
	}{
		{
			name:   "valid_lines",
			fields: []string{"00000", "line1", "mode1", "line2", "mode2", "line3", "mode3", "line4", "mode4", "line5", "mode5"},
		},
		{
			name:    "invalid_dsp_form",
			fields:  []string{"0"},
			wantErr: true,
		},
		{
			name:    "invalid_dsp_form_character",
			fields:  []string{"00A00", "line1", "mode1", "line2", "mode2", "line3", "mode3", "line4", "mode4", "line5", "mode5"},
			wantErr: true,
		},
		{
			name:    "missing_pairs",
			fields:  []string{"00000", "line1"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSTS(tt.fields)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.fields[0], got.DisplayForm)
			assert.Len(t, got.Lines, len(tt.fields[0]))
		})
	}
}

func TestParseGST(t *testing.T) {
	t.Parallel()

	fields := []string{
		"00000",
		"l1", "m1",
		"l2", "m2",
		"l3", "m3",
		"l4", "m4",
		"l5", "m5",
		"Mute", "1", "0",
		"1", "460.1000", "NFM", "0",
		"460.1000", "459.0000", "461.0000", "0", "3",
	}

	got, err := ParseGST(fields)
	require.NoError(t, err)
	assert.Equal(t, "Mute", got.Mute)
	assert.Equal(t, "460.1000", got.Frequency)
	assert.Equal(t, "3", got.FFTSize)
}

func TestParseScannerInfoXML(t *testing.T) {
	t.Parallel()

	raw := []byte(`<?xml version="1.0" encoding="utf-8"?><ScannerInfo Mode="Scan Mode" V_Screen="conventional_scan"><Property VOL="10" SQL="4" Sig="2" Mute="Unmute"/><System Name="Public Safety"/><Department Name="Dispatch"/><ConvFrequency Name="Primary" Freq="460.1000MHz" Hold="On"/></ScannerInfo>`)
	info, err := ParseScannerInfoXML(raw)
	require.NoError(t, err)
	assert.Equal(t, "Scan Mode", info.Mode)
	assert.Equal(t, "conventional_scan", info.VScreen)
	assert.Equal(t, "10", info.Property["VOL"])
	require.Len(t, info.Nodes["System"], 1)
	assert.Equal(t, "Public Safety", info.Nodes["System"][0]["Name"])
}

func TestTelemetryStoreSnapshot(t *testing.T) {
	t.Parallel()

	store := NewTelemetryStore()
	snap := store.Snapshot()
	assert.False(t, snap.Connected)
}

func TestTelemetryStoreUpdateFromSTS(t *testing.T) {
	t.Parallel()

	store := NewTelemetryStore()
	sts := StatusSTS{
		DisplayForm: "00000",
		Lines: []DisplayLine{
			{Text: "Dispatch A", Mode: " "},
		},
	}
	updated := store.UpdateFromSTS(sts)
	assert.Equal(t, cmdSTS, updated.LastSource)
	assert.Equal(t, "Dispatch A", updated.Channel)
}

func TestTelemetryStoreUpdateFromGST(t *testing.T) {
	t.Parallel()

	store := NewTelemetryStore()
	gst := StatusGST{
		StatusSTS: StatusSTS{DisplayForm: "00000"},
		Mute:      "Mute",
		Frequency: "851.0125",
		ColorMode: "0",
		WFMode:    "1",
		Center:    "851.0125",
		Lower:     "850.0000",
		Upper:     "852.0000",
		Mod:       "NFM",
		FFTSize:   "3",
		LED1:      "1",
		LED2:      "0",
	}
	updated := store.UpdateFromGST(gst)
	assert.Equal(t, cmdGST, updated.LastSource)
	assert.Equal(t, "851.0125", updated.Frequency)
	assert.True(t, updated.Mute)
	assert.True(t, updated.Connected)
	assert.False(t, updated.SquelchOpen)
}

func TestTelemetryStoreUpdateFromScannerInfo(t *testing.T) {
	t.Parallel()

	store := NewTelemetryStore()
	info := ScannerInfo{
		Mode:    "Scan Mode",
		VScreen: "conventional_scan",
		Property: map[string]string{
			"VOL":  "11",
			"SQL":  "5",
			"Sig":  "3",
			"Mute": "Unmute",
			"F":    "Off",
		},
		Nodes: map[string][]map[string]string{
			"System":        {{"Name": "County"}},
			"Department":    {{"Name": "Fire"}},
			"ConvFrequency": {{"Name": "Fire Ops", "Freq": "155.2200", "Hold": "On"}},
		},
	}
	updated := store.UpdateFromScannerInfo(info)
	assert.True(t, updated.Connected)
	assert.Equal(t, "County", updated.System)
	assert.Equal(t, "Fire", updated.Department)
	assert.Equal(t, "Fire Ops", updated.Channel)
	assert.Equal(t, 11, updated.Volume)
	assert.True(t, updated.Hold)
	assert.True(t, updated.SquelchOpen)
}

func TestTelemetryStoreUpdateFromScannerInfoHoldOff(t *testing.T) {
	t.Parallel()

	store := NewTelemetryStore()
	info := ScannerInfo{
		Mode:    "Scan Mode",
		VScreen: "conventional_scan",
		Property: map[string]string{
			"Mute": "Mute",
		},
		Nodes: map[string][]map[string]string{
			"ConvFrequency": {{"Name": "Fire Ops", "Freq": "155.2200", "Hold": "Off"}},
		},
	}
	updated := store.UpdateFromScannerInfo(info)
	assert.False(t, updated.Hold)
	assert.False(t, updated.SquelchOpen)
}

func TestParseGCSResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{
			name: "valid_payload",
			raw:  "GCS,CST=4,VOLT=4184mV:100%,CURR=0000mA,TEMP= 27.65C",
		},
		{
			name:    "invalid_payload",
			raw:     "BAD",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseGCSResponse(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 4, got.Status)
			assert.Equal(t, 4184, got.VoltageMV)
			assert.Equal(t, 100, got.CapacityPct)
		})
	}
}
