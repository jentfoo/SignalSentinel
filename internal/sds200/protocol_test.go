package sds200

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  string
		args []string
		want string
	}{
		{name: "command_only", cmd: "mdl", want: "MDL\r"},
		{name: "command_with_args", cmd: "key", args: []string{"1", "P"}, want: "KEY,1,P\r"},
		{name: "trim_spaces", cmd: "  vol ", args: []string{" 12 "}, want: "VOL,12\r"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := buildCommand(tt.cmd, tt.args...)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

func TestParseDelimitedFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		raw        string
		wantCmd    string
		wantFields []string
	}{
		{name: "empty_payload", raw: "", wantCmd: "", wantFields: nil},
		{name: "single_field", raw: "MDL,SDS200\r", wantCmd: "MDL", wantFields: []string{"SDS200"}},
		{name: "many_fields", raw: "SQL,12,extra\r\n", wantCmd: "SQL", wantFields: []string{"12", "extra"}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cmd, fields := parseDelimitedFields([]byte(tt.raw))
			assert.Equal(t, tt.wantCmd, cmd)
			assert.Equal(t, tt.wantFields, fields)
		})
	}
}

func TestParseXMLFragment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		wantSeq int
		wantEOT bool
		wantErr bool
	}{
		{
			name:    "single_packet",
			raw:     `GSI,<XML>,<?xml version="1.0" encoding="utf-8"?><ScannerInfo Mode="Scan"><Property VOL="10"/><Footer No="1" EOT="1"/></ScannerInfo>` + "\r",
			wantSeq: 1,
			wantEOT: true,
		},
		{
			name:    "footer_attributes_reordered",
			raw:     `GSI,<XML>,<?xml version="1.0" encoding="utf-8"?><ScannerInfo Mode="Scan"><Property VOL="10"/><Footer EOT="1" No="2"/></ScannerInfo>` + "\r",
			wantSeq: 2,
			wantEOT: true,
		},
		{
			name:    "missing_footer",
			raw:     `GSI,<XML>,<?xml version="1.0" encoding="utf-8"?><ScannerInfo></ScannerInfo>` + "\r",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			f, err := parseXMLFragment([]byte(tt.raw))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantSeq, f.Seq)
			assert.Equal(t, tt.wantEOT, f.EOT)
			assert.NotEmpty(t, f.RootName)
		})
	}
}

func TestXMLReassembler(t *testing.T) {
	t.Parallel()

	t.Run("complete_sequence", func(t *testing.T) {
		x := newXMLReassembler()
		x.Add(&xmlFragment{RootOpenTag: "<GLT>", RootName: "GLT", Body: `<FL Index="0"/>`, Seq: 1, EOT: false})
		x.Add(&xmlFragment{RootOpenTag: "<GLT>", RootName: "GLT", Body: `<FL Index="1"/>`, Seq: 2, EOT: true})
		assert.True(t, x.Complete())
		assert.Empty(t, x.MissingSequences())
		assert.Contains(t, string(x.Bytes()), `<FL Index="0"/>`)
		assert.Contains(t, string(x.Bytes()), `<FL Index="1"/>`)
	})

	t.Run("missing_sequence", func(t *testing.T) {
		x := newXMLReassembler()
		x.Add(&xmlFragment{RootOpenTag: "<GLT>", RootName: "GLT", Body: `<FL Index="0"/>`, Seq: 1, EOT: false})
		x.Add(&xmlFragment{RootOpenTag: "<GLT>", RootName: "GLT", Body: `<FL Index="2"/>`, Seq: 3, EOT: true})
		assert.False(t, x.Complete())
		assert.Equal(t, []int{2}, x.MissingSequences())
		assert.Nil(t, x.Bytes())
	})
}

func TestParseXMLNode(t *testing.T) {
	t.Parallel()

	raw := []byte(`<?xml version="1.0" encoding="utf-8"?><ScannerInfo Mode="Scan"><Property VOL="10"/><System Name="City"/></ScannerInfo>`)
	node, err := parseXMLNode(raw)
	require.NoError(t, err)
	assert.Equal(t, "ScannerInfo", node.XMLName.Local)
	assert.Equal(t, "Scan", node.Attrs["Mode"])

	child, ok := nodeFirstChildByName(node, "Property")
	require.True(t, ok)
	assert.Equal(t, "10", child.Attrs["VOL"])
}

func TestParseIntDefault(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 12, parseIntDefault("12", 3))
	assert.Equal(t, 3, parseIntDefault("x", 3))
}

func TestParseFloatDefault(t *testing.T) {
	t.Parallel()

	assert.InDelta(t, 12.5, parseFloatDefault("12.5", 2.0), 0.000001)
	assert.InDelta(t, 2.0, parseFloatDefault("x", 2.0), 0.000001)
}

func TestParseBoolOnOff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "on_value", in: "On", want: true},
		{name: "one_value", in: "1", want: true},
		{name: "off_value", in: "Off", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseBoolOnOff(tt.in))
		})
	}
}

func TestParseTimeLayouts(t *testing.T) {
	t.Parallel()

	t.Run("first_layout", func(t *testing.T) {
		got, err := parseTimeLayouts("2026-03-07 12:00:00", "2006-01-02 15:04:05", time.RFC3339)
		require.NoError(t, err)
		assert.Equal(t, 2026, got.Year())
	})

	t.Run("no_layout_match", func(t *testing.T) {
		_, err := parseTimeLayouts("bad", time.RFC3339)
		require.Error(t, err)
	})
}
