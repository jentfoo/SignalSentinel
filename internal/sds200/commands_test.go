package sds200

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCommandTestClient(t *testing.T, handler func(req string) []string) (*Client, *udpMockServer) {
	t.Helper()
	server := startUDPMockServer(t, handler)
	host, port := server.hostPort()
	client, err := NewClient(ClientConfig{
		Address:         host,
		ControlPort:     port,
		ResponseTimeout: 500 * time.Millisecond,
		Retries:         2,
		QueueSize:       32,
		ReadTimeout:     100 * time.Millisecond,
		WriteTimeout:    100 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client, server
}

func quickKeysPayload(prefix ...string) string {
	fields := append([]string{}, prefix...)
	for i := 0; i < 100; i++ {
		fields = append(fields, strconv.Itoa(i%3))
	}
	return strings.Join(fields, ",")
}

func TestCommandResponseIsOK(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		fields []string
		want   bool
	}{
		{name: "ok_uppercase", fields: []string{"OK"}, want: true},
		{name: "ok_lowercase", fields: []string{"ok"}, want: true},
		{name: "ok_with_whitespace", fields: []string{" OK "}, want: true},
		{name: "ng_response", fields: []string{"NG"}, want: false},
		{name: "empty_fields", fields: nil, want: false},
		{name: "data_response", fields: []string{"SDS200"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := CommandResponse{Command: "TEST", Fields: tt.fields}
			assert.Equal(t, tt.want, resp.IsOK())
		})
	}
}

func TestCommandResponseIsNG(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		fields []string
		want   bool
	}{
		{name: "ng_response", fields: []string{"NG"}, want: true},
		{name: "err_response", fields: []string{"ERR"}, want: true},
		{name: "ng_with_whitespace", fields: []string{" ng "}, want: true},
		{name: "ok_response", fields: []string{"OK"}, want: false},
		{name: "empty_fields", fields: nil, want: false},
		{name: "data_response", fields: []string{"SDS200"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := CommandResponse{Command: "TEST", Fields: tt.fields}
			assert.Equal(t, tt.want, resp.IsNG())
		})
	}
}

func TestRequireOK(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		resp    CommandResponse
		wantErr bool
	}{
		{name: "ok_response", resp: CommandResponse{Command: "VOL", Fields: []string{"OK"}}, wantErr: false},
		{name: "ng_response", resp: CommandResponse{Command: "VOL", Fields: []string{"NG"}}, wantErr: true},
		{name: "err_response", resp: CommandResponse{Command: "URC", Fields: []string{"ERR", "0001"}}, wantErr: true},
		{name: "empty_fields", resp: CommandResponse{Command: "VOL", Fields: nil}, wantErr: true},
		{name: "unexpected_data", resp: CommandResponse{Command: "VOL", Fields: []string{"12"}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireOK(tt.resp)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestParseQuickKeys(t *testing.T) {
	t.Parallel()

	t.Run("valid_keys", func(t *testing.T) {
		fields := make([]string, 100)
		for i := range fields {
			fields[i] = strconv.Itoa(i % 3)
		}
		state, err := parseQuickKeys(fields, 0)
		require.NoError(t, err)
		assert.Equal(t, 0, state[0])
		assert.Equal(t, 1, state[1])
		assert.Equal(t, 2, state[2])
	})

	t.Run("too_short", func(t *testing.T) {
		_, err := parseQuickKeys([]string{"0", "1"}, 0)
		require.Error(t, err)
	})

	t.Run("with_offset", func(t *testing.T) {
		fields := make([]string, 102)
		fields[0] = "99"
		fields[1] = "88"
		for i := 2; i < 102; i++ {
			fields[i] = strconv.Itoa(i - 2)
		}
		state, err := parseQuickKeys(fields, 2)
		require.NoError(t, err)
		assert.Equal(t, 0, state[0])
		assert.Equal(t, 1, state[1])
	})
}

func TestExecute(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == "ABC" {
			return []string{"ABC,OK\r"}
		}
		return nil
	})

	resp, err := client.Execute("abc")
	require.NoError(t, err)
	assert.Equal(t, "ABC", resp.Command)
	assert.True(t, resp.IsOK())
}

func TestModel(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdMDL {
			return []string{"MDL,SDS200\r"}
		}
		return nil
	})

	model, err := client.Model()
	require.NoError(t, err)
	assert.Equal(t, "SDS200", model)
}

func TestFirmwareVersion(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdVER {
			return []string{"VER,1.02.03\r"}
		}
		return nil
	})

	ver, err := client.FirmwareVersion()
	require.NoError(t, err)
	assert.Equal(t, "1.02.03", ver)
}

func TestKeyPress(t *testing.T) {
	t.Parallel()

	client, server := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdKEY {
			return []string{"KEY,OK\r"}
		}
		return nil
	})

	require.NoError(t, client.KeyPress("1", KeyModePress))
	req := <-server.requests
	assert.Equal(t, "KEY,1,P\r", req)
}

func TestQuickSearchHold(t *testing.T) {
	t.Parallel()

	client, server := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdQSH {
			return []string{"QSH,OK\r"}
		}
		return nil
	})

	require.NoError(t, client.QuickSearchHold(4060000))
	req := <-server.requests
	assert.Equal(t, "QSH,4060000\r", req)
}

func TestStatus(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdSTS {
			return []string{"STS,00000,l1,m1,l2,m2,l3,m3,l4,m4,l5,m5\r"}
		}
		return nil
	})

	sts, err := client.Status()
	require.NoError(t, err)
	assert.Len(t, sts.Lines, 5)
}

func TestScannerStatus(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdGST {
			return []string{"GST,00000,l1,m1,l2,m2,l3,m3,l4,m4,l5,m5,Mute,1,0,1,851.0125,NFM,0,851.0125,850.0000,852.0000,0,3\r"}
		}
		return nil
	})

	gst, err := client.ScannerStatus()
	require.NoError(t, err)
	assert.Equal(t, "851.0125", gst.Frequency)
}

func TestGetScannerInfo(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdGSI {
			return []string{splitXMLResponse("GSI", scannerInfoXML("Scan Mode"))}
		}
		return nil
	})

	info, err := client.GetScannerInfo()
	require.NoError(t, err)
	assert.Equal(t, "Scan Mode", info.Mode)
}

func TestGetList(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdGLT {
			return []string{splitXMLResponse("GLT", gltXML())}
		}
		return nil
	})

	node, err := client.GetList("FL")
	require.NoError(t, err)
	assert.Equal(t, "GLT", node.XMLName.Local)
}

func TestJumpNumberTag(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdJNT {
			return []string{"JNT,OK\r"}
		}
		return nil
	})

	require.NoError(t, client.JumpNumberTag(1, 2, 3))
}

func TestNext(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdNXT {
			return []string{"NXT,OK\r"}
		}
		return nil
	})
	require.NoError(t, client.Next("SYS", "1", "", 2))
}

func TestPrevious(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdPRV {
			return []string{"PRV,OK\r"}
		}
		return nil
	})
	require.NoError(t, client.Previous("SYS", "1", "", 2))
}

func TestHold(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdHLD {
			return []string{"HLD,OK\r"}
		}
		return nil
	})
	require.NoError(t, client.Hold("SYS", "1", ""))
}

func TestAvoid(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdAVD {
			return []string{"AVD,OK\r"}
		}
		return nil
	})
	require.NoError(t, client.Avoid("SYS", "1", "", 1))
}

func TestGetFavoritesQuickKeys(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdFQK {
			return []string{"FQK," + quickKeysPayload() + "\r"}
		}
		return nil
	})

	state, err := client.GetFavoritesQuickKeys()
	require.NoError(t, err)
	assert.Equal(t, 0, state[0])
	assert.Equal(t, 1, state[1])
}

func TestSetFavoritesQuickKeys(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdFQK {
			return []string{"FQK,OK\r"}
		}
		return nil
	})

	var state QuickKeyState
	for i := 0; i < 100; i++ {
		state[i] = i % 3
	}
	require.NoError(t, client.SetFavoritesQuickKeys(state))
}

func TestGetSystemQuickKeys(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdSQK {
			return []string{"SQK," + quickKeysPayload("1", "2") + "\r"}
		}
		return nil
	})

	state, err := client.GetSystemQuickKeys(1)
	require.NoError(t, err)
	assert.Equal(t, 0, state[0])
}

func TestSetSystemQuickKeys(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdSQK {
			return []string{"SQK,OK\r"}
		}
		return nil
	})

	var state QuickKeyState
	require.NoError(t, client.SetSystemQuickKeys(1, state))
}

func TestGetDepartmentQuickKeys(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdDQK {
			return []string{"DQK," + quickKeysPayload("1", "2") + "\r"}
		}
		return nil
	})

	state, err := client.GetDepartmentQuickKeys(1, 2)
	require.NoError(t, err)
	assert.Equal(t, 0, state[0])
}

func TestSetDepartmentQuickKeys(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdDQK {
			return []string{"DQK,OK\r"}
		}
		return nil
	})

	var state QuickKeyState
	require.NoError(t, client.SetDepartmentQuickKeys(1, 2, state))
}

func TestGetServiceTypes(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdSVC {
			values := make([]string, 47)
			for i := 0; i < 47; i++ {
				values[i] = strconv.Itoa(i % 2)
			}
			return []string{"SVC," + strings.Join(values, ",") + "\r"}
		}
		return nil
	})

	values, err := client.GetServiceTypes()
	require.NoError(t, err)
	assert.Len(t, values, 47)
}

func TestSetServiceTypes(t *testing.T) {
	t.Parallel()

	t.Run("valid_length", func(t *testing.T) {
		client, _ := newCommandTestClient(t, func(req string) []string {
			if readRequestCommand(req) == cmdSVC {
				return []string{"SVC,OK\r"}
			}
			return nil
		})
		values := make([]int, 47)
		require.NoError(t, client.SetServiceTypes(values))
	})

	t.Run("wrong_length", func(t *testing.T) {
		client, _ := newCommandTestClient(t, func(req string) []string {
			return nil
		})
		require.Error(t, client.SetServiceTypes(make([]int, 10)))
	})
}

func TestJumpMode(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdJPM {
			return []string{"JPM,OK\r"}
		}
		return nil
	})
	require.NoError(t, client.JumpMode("SCN_MODE", "0"))
}

func TestGetDateTime(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdDTM {
			return []string{"DTM,1,2026,03,07,12,30,15,1\r"}
		}
		return nil
	})

	dtm, err := client.GetDateTime()
	require.NoError(t, err)
	assert.Equal(t, 2026, dtm.Time.Year())
	assert.Equal(t, 1, dtm.DaylightSaving)
}

func TestSetDateTime(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdDTM {
			return []string{"DTM,OK\r"}
		}
		return nil
	})

	require.NoError(t, client.SetDateTime(1, time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)))
}

func TestGetLocationRange(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdLCR {
			return []string{"LCR,39.1234,-104.5678,10\r"}
		}
		return nil
	})

	loc, err := client.GetLocationRange()
	require.NoError(t, err)
	assert.Equal(t, "39.1234", loc.Latitude)
}

func TestSetLocationRange(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdLCR {
			return []string{"LCR,OK\r"}
		}
		return nil
	})
	require.NoError(t, client.SetLocationRange("39.1", "-104.5", "10"))
}

func TestAnalyzeStart(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdAST {
			return []string{"AST,OK\r"}
		}
		return nil
	})

	resp, err := client.AnalyzeStart("SYSTEM_STATUS", "1")
	require.NoError(t, err)
	assert.True(t, resp.IsOK())
}

func TestAnalyzePauseResume(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdAPR {
			return []string{"APR,OK\r"}
		}
		return nil
	})
	require.NoError(t, client.AnalyzePauseResume("SYSTEM_STATUS"))
}

func TestPushWaterfallFFT(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdPWF {
			return []string{"PWF,1,2,3\r"}
		}
		return nil
	})

	resp, err := client.PushWaterfallFFT(1, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2", "3"}, resp.Fields)
}

func TestGetWaterfallFFT(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdGWF {
			return []string{"GWF,1,2,3\r"}
		}
		return nil
	})

	fft, err := client.GetWaterfallFFT(1, true)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, fft)
}

func TestGetWaterfallFFTBinary(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdGWF {
			return []string{"GW2,\x01\x02\x03\r"}
		}
		return nil
	})

	data, err := client.GetWaterfallFFTBinary(1, true)
	require.NoError(t, err)
	assert.Equal(t, []byte{0x01, 0x02, 0x03}, data)
}

func TestGetWaterfallFFTBinaryPreservesCRLF(t *testing.T) {
	t.Parallel()

	// Verify that 0x0D and 0x0A bytes within the binary payload are preserved.
	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdGWF {
			return []string{"GW2,\x01\x0D\x0A\x02\r"}
		}
		return nil
	})

	data, err := client.GetWaterfallFFTBinary(1, true)
	require.NoError(t, err)
	assert.Equal(t, []byte{0x01, 0x0D, 0x0A, 0x02}, data)
}

func TestGetWaterfallFFTBinaryAcceptsGWFResponse(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdGWF {
			return []string{"GWF,\x04\x05\x06\r"}
		}
		return nil
	})

	data, err := client.GetWaterfallFFTBinary(1, true)
	require.NoError(t, err)
	assert.Equal(t, []byte{0x04, 0x05, 0x06}, data)
}

func TestStartPushScannerInfoNoImmediateResponse(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdPSI {
			return nil
		}
		return nil
	})

	require.NoError(t, client.StartPushScannerInfo(5000))
}

func TestUserRecordStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		resp      string
		want      int
		wantError string
	}{
		{name: "status_ok", resp: "URC,1\r", want: 1},
		{name: "status_err", resp: "URC,ERR,0001\r", want: 0, wantError: "0001"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			client, _ := newCommandTestClient(t, func(req string) []string {
				if readRequestCommand(req) == cmdURC {
					return []string{tt.resp}
				}
				return nil
			})
			status, err := client.UserRecordStatus()
			require.NoError(t, err)
			assert.Equal(t, tt.want, status.Status)
			assert.Equal(t, tt.wantError, status.ErrorCode)
		})
	}
}

func TestSetUserRecord(t *testing.T) {
	t.Parallel()

	t.Run("enable_ok", func(t *testing.T) {
		client, _ := newCommandTestClient(t, func(req string) []string {
			if readRequestCommand(req) == cmdURC {
				return []string{"URC,OK\r"}
			}
			return nil
		})
		require.NoError(t, client.SetUserRecord(true))
	})

	t.Run("enable_err", func(t *testing.T) {
		client, _ := newCommandTestClient(t, func(req string) []string {
			if readRequestCommand(req) == cmdURC {
				return []string{"URC,ERR,0001\r"}
			}
			return nil
		})
		require.Error(t, client.SetUserRecord(true))
	})
}

func TestEnterMenu(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdMNU {
			return []string{"MNU,OK\r"}
		}
		return nil
	})
	require.NoError(t, client.EnterMenu("TOP", ""))
}

func TestMenuStatus(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdMSI {
			xml := `<?xml version="1.0" encoding="utf-8"?><MSI Name="Menu"><MenuItem Name="A"/><Footer No="1" EOT="1"/></MSI>`
			return []string{splitXMLResponse("MSI", xml)}
		}
		return nil
	})

	node, err := client.MenuStatus()
	require.NoError(t, err)
	assert.Equal(t, "MSI", node.XMLName.Local)
}

func TestMenuSetValue(t *testing.T) {
	t.Parallel()

	client, server := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdMSV {
			return []string{"MSV,OK\r"}
		}
		return nil
	})

	require.NoError(t, client.MenuSetValue("a,b"))
	req := <-server.requests
	assert.Contains(t, req, "a\tb")
}

func TestMenuBack(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdMSB {
			return []string{"MSB,OK\r"}
		}
		return nil
	})
	require.NoError(t, client.MenuBack("RETURN_PREVOUS_MODE"))
}

func TestKeepAlive(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdKAL {
			return nil
		}
		return nil
	})
	require.NoError(t, client.KeepAlive())
}

func TestPowerOff(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdPOF {
			return []string{"POF,OK\r"}
		}
		return nil
	})
	require.NoError(t, client.PowerOff())
}

func TestGetChargeStatus(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdGCS {
			return []string{"GCS,CST=4,VOLT=4184mV:100%,CURR=0000mA,TEMP= 27.65C\r"}
		}
		return nil
	})

	status, err := client.GetChargeStatus()
	require.NoError(t, err)
	assert.Equal(t, 4, status.Status)
}

func TestGetVolume(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdVOL {
			return []string{"VOL,12\r"}
		}
		return nil
	})

	level, err := client.GetVolume()
	require.NoError(t, err)
	assert.Equal(t, 12, level)
}

func TestSetVolume(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		level   int
		wantErr bool
	}{
		{name: "valid_level", level: 10},
		{name: "invalid_level", level: 30, wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			client, _ := newCommandTestClient(t, func(req string) []string {
				if readRequestCommand(req) == cmdVOL {
					return []string{"VOL\r"}
				}
				return nil
			})
			err := client.SetVolume(tt.level)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestSetVolumeNG(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdVOL {
			return []string{"VOL,NG\r"}
		}
		return nil
	})

	require.Error(t, client.SetVolume(10))
}

func TestGetSquelch(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdSQL {
			return []string{"SQL,5\r"}
		}
		return nil
	})

	level, err := client.GetSquelch()
	require.NoError(t, err)
	assert.Equal(t, 5, level)
}

func TestSetSquelch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		level   int
		wantErr bool
	}{
		{name: "valid_level", level: 5},
		{name: "invalid_level", level: 20, wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			client, _ := newCommandTestClient(t, func(req string) []string {
				if readRequestCommand(req) == cmdSQL {
					return []string{"SQL\r"}
				}
				return nil
			})
			err := client.SetSquelch(tt.level)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestSetSquelchNG(t *testing.T) {
	t.Parallel()

	client, _ := newCommandTestClient(t, func(req string) []string {
		if readRequestCommand(req) == cmdSQL {
			return []string{"SQL,NG\r"}
		}
		return nil
	})

	require.Error(t, client.SetSquelch(5))
}
