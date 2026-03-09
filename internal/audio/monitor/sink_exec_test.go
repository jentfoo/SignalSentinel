package monitor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseAplayDeviceList(t *testing.T) {
	t.Parallel()

	raw := `null
    Discard all samples (playback) or generate zero samples (capture)
default
    Default Audio Device
sysdefault:CARD=PCH
    HDA Intel PCH
front:CARD=PCH,DEV=0
    HDA Intel PCH
`
	got := parseAplayDeviceList(raw)
	assert.Equal(t, []string{"null", "default", "sysdefault:CARD=PCH", "front:CARD=PCH,DEV=0"}, got)
}
