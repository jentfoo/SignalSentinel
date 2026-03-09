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
		want    []DisplayLine
		wantErr bool
	}{
		{
			name:   "valid_lines",
			fields: []string{"00000", "line1", "mode1", "line2", "mode2", "line3", "mode3", "line4", "mode4", "line5", "mode5"},
			want: []DisplayLine{
				{Text: "line1", Mode: "mode1"},
				{Text: "line2", Mode: "mode2"},
				{Text: "line3", Mode: "mode3"},
				{Text: "line4", Mode: "mode4"},
				{Text: "line5", Mode: "mode5"},
			},
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
			assert.Equal(t, tt.want, got.Lines)
		})
	}
}

func TestParseGST(t *testing.T) {
	t.Parallel()

	t.Run("valid_fields", func(t *testing.T) {
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
		assert.Equal(t, "NFM", got.Mod)
		assert.Equal(t, "460.1000", got.Center)
		assert.Equal(t, "459.0000", got.Lower)
		assert.Equal(t, "461.0000", got.Upper)
		assert.Equal(t, "3", got.FFTSize)
		assert.Equal(t, "1", got.LED1)
		assert.Equal(t, "0", got.LED2)
	})

	t.Run("insufficient_fields", func(t *testing.T) {
		fields := []string{
			"00000",
			"l1", "m1",
			"l2", "m2",
			"l3", "m3",
			"l4", "m4",
			"l5", "m5",
		}
		_, err := ParseGST(fields)
		require.Error(t, err)
	})
}

func TestParseScannerInfoXML(t *testing.T) {
	t.Parallel()

	t.Run("conventional_frequency", func(t *testing.T) {
		raw := []byte(`<?xml version="1.0" encoding="utf-8"?><ScannerInfo Mode="Scan Mode" V_Screen="conventional_scan"><Property VOL="10" SQL="4" Sig="2" Mute="Unmute"/><System Name="Public Safety"/><Department Name="Dispatch"/><ConvFrequency Name="Primary" Freq="460.1000MHz" Hold="On"/></ScannerInfo>`)
		info, err := ParseScannerInfoXML(raw)
		require.NoError(t, err)
		assert.Equal(t, "Scan Mode", info.Mode)
		assert.Equal(t, "conventional_scan", info.VScreen)
		assert.Equal(t, "10", info.Property["VOL"])
		require.Len(t, info.Nodes["System"], 1)
		assert.Equal(t, "Public Safety", info.Nodes["System"][0]["Name"])
	})

	t.Run("trunked_tgid", func(t *testing.T) {
		raw := []byte(`<?xml version="1.0" encoding="utf-8"?><ScannerInfo Mode="Scan Mode" V_Screen="trunk_scan"><Property VOL="12" SQL="3" Sig="5" Mute="Unmute"/><System Name="County P25"/><Department Name="Law Enforcement"/><TGID Name="Dispatch 1" TGID="100" Hold="On"/></ScannerInfo>`)
		info, err := ParseScannerInfoXML(raw)
		require.NoError(t, err)
		assert.Equal(t, "trunk_scan", info.VScreen)
		require.Len(t, info.Nodes["TGID"], 1)
		assert.Equal(t, "Dispatch 1", info.Nodes["TGID"][0]["Name"])
		assert.Equal(t, "100", info.Nodes["TGID"][0]["TGID"])
	})

	t.Run("wrong_root_element", func(t *testing.T) {
		raw := []byte(`<?xml version="1.0" encoding="utf-8"?><GLT><FL Index="0"/></GLT>`)
		_, err := ParseScannerInfoXML(raw)
		require.Error(t, err)
	})
}

func TestTelemetryStoreSnapshot(t *testing.T) {
	t.Parallel()

	t.Run("defaults_disconnected", func(t *testing.T) {
		store := NewTelemetryStore()
		snap := store.Snapshot()
		assert.False(t, snap.Connected)
	})
}

func TestTelemetryStoreUpdateFromSTS(t *testing.T) {
	t.Parallel()

	t.Run("updates_channel", func(t *testing.T) {
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
	})
}

func TestTelemetryStoreUpdateFromGST(t *testing.T) {
	t.Parallel()

	t.Run("updates_status", func(t *testing.T) {
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
	})
}

func TestTelemetryStoreUpdateFromScannerInfo(t *testing.T) {
	t.Parallel()

	t.Run("conventional", func(t *testing.T) {
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
				"ConvFrequency": {{"Name": "Fire Ops", "Freq": "155.2200", "Index": "120", "Hold": "On", "Avoid": "Off"}},
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
		assert.Equal(t, "CFREQ", updated.HoldTarget.Keyword)
		assert.Equal(t, "120", updated.HoldTarget.Arg1)
		assert.True(t, updated.AvoidKnown)
		assert.False(t, updated.Avoided)
	})
}

func TestTelemetryStoreUpdateFromScannerInfoTGID(t *testing.T) {
	t.Parallel()

	t.Run("trunked_tgid", func(t *testing.T) {
		store := NewTelemetryStore()
		info := ScannerInfo{
			Mode:    "Scan Mode",
			VScreen: "trunk_scan",
			Property: map[string]string{
				"VOL":  "15",
				"SQL":  "3",
				"Sig":  "5",
				"Mute": "Unmute",
			},
			Nodes: map[string][]map[string]string{
				"System":     {{"Name": "County P25"}},
				"Department": {{"Name": "Law Enforcement"}},
				"TGID":       {{"Name": "Dispatch 1", "TGID": "100", "Site": "2", "Hold": "On", "Avoid": "T-Avoid"}},
			},
		}
		updated := store.UpdateFromScannerInfo(info)
		assert.True(t, updated.Connected)
		assert.Equal(t, "County P25", updated.System)
		assert.Equal(t, "Law Enforcement", updated.Department)
		assert.Equal(t, "Dispatch 1", updated.Channel)
		assert.Equal(t, "100", updated.Talkgroup)
		assert.True(t, updated.Hold)
		assert.Equal(t, 15, updated.Volume)
		assert.Equal(t, 5, updated.Signal)
		assert.Equal(t, "TGID", updated.HoldTarget.Keyword)
		assert.Equal(t, "100", updated.HoldTarget.Arg1)
		assert.Equal(t, "2", updated.HoldTarget.Arg2)
		assert.True(t, updated.AvoidKnown)
		assert.True(t, updated.Avoided)
	})
}

func TestTelemetryStoreUpdateFromScannerInfoAvoidFallsThroughEmptyNodeValue(t *testing.T) {
	t.Parallel()

	t.Run("empty_avoid_fallback", func(t *testing.T) {
		store := NewTelemetryStore()
		info := ScannerInfo{
			Mode:    "Scan Mode",
			VScreen: "trunk_scan",
			Property: map[string]string{
				"Mute": "Unmute",
			},
			Nodes: map[string][]map[string]string{
				"ConvFrequency": {{"Name": "Placeholder", "Avoid": ""}},
				"TGID":          {{"Name": "Dispatch 1", "TGID": "100", "Site": "2", "Hold": "On", "Avoid": "T-Avoid"}},
			},
		}
		updated := store.UpdateFromScannerInfo(info)
		assert.True(t, updated.AvoidKnown)
		assert.True(t, updated.Avoided)
	})
}

func TestTelemetryStoreUpdateFromScannerInfoHoldOff(t *testing.T) {
	t.Parallel()

	t.Run("hold_off", func(t *testing.T) {
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
	})
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
			assert.Equal(t, 0, got.CurrentMA)
			assert.InDelta(t, 27.65, got.TempC, 0.001)
		})
	}
}

func TestIsTransmissionActive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status RuntimeStatus
		want   bool
	}{
		{
			name:   "disconnected",
			status: RuntimeStatus{Connected: false, SquelchOpen: true, Signal: 5},
			want:   false,
		},
		{
			name:   "squelch_open",
			status: RuntimeStatus{Connected: true, SquelchOpen: true},
			want:   true,
		},
		{
			name:   "signal_without_mute",
			status: RuntimeStatus{Connected: true, Signal: 3, Mute: false},
			want:   true,
		},
		{
			name:   "signal_muted",
			status: RuntimeStatus{Connected: true, Signal: 3, Mute: true},
			want:   false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsTransmissionActive(tt.status))
		})
	}
}
