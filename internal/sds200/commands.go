package sds200

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type CommandMode int

const (
	ModeNormal CommandMode = iota
	ModeXML
	ModeBinary
	ModeNoResponse
)

type CommandSpec struct {
	Command string
	Mode    CommandMode
}

var commandSpecs = map[string]CommandSpec{
	cmdMDL: {Command: cmdMDL, Mode: ModeNormal},
	cmdVER: {Command: cmdVER, Mode: ModeNormal},
	cmdKEY: {Command: cmdKEY, Mode: ModeNormal},
	cmdQSH: {Command: cmdQSH, Mode: ModeNormal},
	cmdSTS: {Command: cmdSTS, Mode: ModeNormal},
	cmdJNT: {Command: cmdJNT, Mode: ModeNormal},
	cmdNXT: {Command: cmdNXT, Mode: ModeNormal},
	cmdPRV: {Command: cmdPRV, Mode: ModeNormal},
	cmdFQK: {Command: cmdFQK, Mode: ModeNormal},
	cmdSQK: {Command: cmdSQK, Mode: ModeNormal},
	cmdDQK: {Command: cmdDQK, Mode: ModeNormal},
	cmdPSI: {Command: cmdPSI, Mode: ModeXML},
	cmdGSI: {Command: cmdGSI, Mode: ModeXML},
	cmdGLT: {Command: cmdGLT, Mode: ModeXML},
	cmdHLD: {Command: cmdHLD, Mode: ModeNormal},
	cmdAVD: {Command: cmdAVD, Mode: ModeNormal},
	cmdSVC: {Command: cmdSVC, Mode: ModeNormal},
	cmdJPM: {Command: cmdJPM, Mode: ModeNormal},
	cmdDTM: {Command: cmdDTM, Mode: ModeNormal},
	cmdLCR: {Command: cmdLCR, Mode: ModeNormal},
	cmdAST: {Command: cmdAST, Mode: ModeNormal},
	cmdAPR: {Command: cmdAPR, Mode: ModeNormal},
	cmdURC: {Command: cmdURC, Mode: ModeNormal},
	cmdMNU: {Command: cmdMNU, Mode: ModeNormal},
	cmdMSI: {Command: cmdMSI, Mode: ModeXML},
	cmdMSV: {Command: cmdMSV, Mode: ModeNormal},
	cmdMSB: {Command: cmdMSB, Mode: ModeNormal},
	cmdGST: {Command: cmdGST, Mode: ModeNormal},
	cmdPWF: {Command: cmdPWF, Mode: ModeNormal},
	cmdGWF: {Command: cmdGWF, Mode: ModeNormal},
	cmdKAL: {Command: cmdKAL, Mode: ModeNoResponse},
	cmdPOF: {Command: cmdPOF, Mode: ModeNormal},
	cmdGCS: {Command: cmdGCS, Mode: ModeNormal},
	cmdVOL: {Command: cmdVOL, Mode: ModeNormal},
	cmdSQL: {Command: cmdSQL, Mode: ModeNormal},
}

type CommandResponse struct {
	Command string
	Fields  []string
	XML     []byte
	Raw     []byte
}

func (r CommandResponse) IsOK() bool {
	if len(r.Fields) == 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.Fields[0]), "OK")
}

func (r CommandResponse) IsNG() bool {
	if len(r.Fields) == 0 {
		return false
	}
	v := strings.ToUpper(strings.TrimSpace(r.Fields[0]))
	return v == "NG" || v == "ERR"
}

type KeyMode string

const (
	KeyModePress   KeyMode = "P"
	KeyModeLong    KeyMode = "L"
	KeyModeHold    KeyMode = "H"
	KeyModeRelease KeyMode = "R"
)

type QuickKeyState [100]int

type DateTimeStatus struct {
	DaylightSaving int
	Time           time.Time
	RTCStatus      int
}

type LocationRange struct {
	Latitude  string
	Longitude string
	Range     string
}

type URCStatus struct {
	Status    int
	ErrorCode string
}

func (c *Client) Execute(command string, args ...string) (CommandResponse, error) {
	spec, ok := commandSpecs[strings.ToUpper(command)]
	if !ok {
		spec = CommandSpec{Command: strings.ToUpper(command), Mode: ModeNormal}
	}
	return c.execute(spec, args...)
}

func (c *Client) Model() (string, error) {
	resp, err := c.execute(commandSpecs[cmdMDL])
	if err != nil {
		return "", err
	} else if len(resp.Fields) < 1 {
		return "", errors.New("mdl response missing model")
	}
	return resp.Fields[0], nil
}

func (c *Client) FirmwareVersion() (string, error) {
	resp, err := c.execute(commandSpecs[cmdVER])
	if err != nil {
		return "", err
	} else if len(resp.Fields) < 1 {
		return "", errors.New("ver response missing version")
	}
	return resp.Fields[0], nil
}

func (c *Client) KeyPress(code string, mode KeyMode) error {
	return c.executeOK(commandSpecs[cmdKEY], code, string(mode))
}

func (c *Client) QuickSearchHold(freqHz int) error {
	return c.executeOK(commandSpecs[cmdQSH], strconv.Itoa(freqHz))
}

func (c *Client) Status() (StatusSTS, error) {
	resp, err := c.execute(commandSpecs[cmdSTS])
	if err != nil {
		return StatusSTS{}, err
	}
	return ParseSTS(resp.Fields)
}

func (c *Client) ScannerStatus() (StatusGST, error) {
	resp, err := c.execute(commandSpecs[cmdGST])
	if err != nil {
		return StatusGST{}, err
	}
	return ParseGST(resp.Fields)
}

func (c *Client) StartPushScannerInfo(intervalMS int) error {
	args := []string{}
	if intervalMS > 0 {
		args = append(args, strconv.Itoa(intervalMS))
	}
	return c.executeOK(CommandSpec{Command: cmdPSI, Mode: ModeNormal}, args...)
}

func (c *Client) GetScannerInfo() (ScannerInfo, error) {
	resp, err := c.execute(commandSpecs[cmdGSI])
	if err != nil {
		return ScannerInfo{}, err
	}
	return ParseScannerInfoXML(resp.XML)
}

func (c *Client) GetList(listType string, index ...int) (XMLNode, error) {
	args := []string{listType}
	if len(index) > 0 {
		args = append(args, strconv.Itoa(index[0]))
	}
	resp, err := c.execute(commandSpecs[cmdGLT], args...)
	if err != nil {
		return XMLNode{}, err
	}
	return parseXMLNode(resp.XML)
}

func (c *Client) JumpNumberTag(flTag, sysTag, chanTag int) error {
	return c.executeOK(commandSpecs[cmdJNT], strconv.Itoa(flTag), strconv.Itoa(sysTag), strconv.Itoa(chanTag))
}

func (c *Client) Next(tkw, x1, x2 string, count int) error {
	return c.executeOK(commandSpecs[cmdNXT], tkw, x1, x2, strconv.Itoa(count))
}

func (c *Client) Previous(tkw, x1, x2 string, count int) error {
	return c.executeOK(commandSpecs[cmdPRV], tkw, x1, x2, strconv.Itoa(count))
}

func (c *Client) Hold(tkw, x1, x2 string) error {
	return c.executeOK(commandSpecs[cmdHLD], tkw, x1, x2)
}

func (c *Client) Avoid(tkw, x1, x2 string, status int) error {
	return c.executeOK(commandSpecs[cmdAVD], tkw, x1, x2, strconv.Itoa(status))
}

func parseQuickKeys(fields []string, offset int) (QuickKeyState, error) {
	var state QuickKeyState
	if len(fields) < offset+100 {
		return state, fmt.Errorf("quick key response too short: %d", len(fields))
	}
	for i := 0; i < 100; i++ {
		state[i] = parseIntDefault(fields[offset+i], 0)
	}
	return state, nil
}

func (c *Client) GetFavoritesQuickKeys() (QuickKeyState, error) {
	resp, err := c.execute(commandSpecs[cmdFQK])
	if err != nil {
		return QuickKeyState{}, err
	}
	return parseQuickKeys(resp.Fields, 0)
}

func (c *Client) SetFavoritesQuickKeys(state QuickKeyState) error {
	args := make([]string, 0, 100)
	for i := 0; i < 100; i++ {
		args = append(args, strconv.Itoa(state[i]))
	}
	return c.executeOK(commandSpecs[cmdFQK], args...)
}

func (c *Client) GetSystemQuickKeys(favQK int) (QuickKeyState, error) {
	resp, err := c.execute(commandSpecs[cmdSQK], strconv.Itoa(favQK))
	if err != nil {
		return QuickKeyState{}, err
	}
	return parseQuickKeys(resp.Fields, 2)
}

func (c *Client) SetSystemQuickKeys(favQK int, state QuickKeyState) error {
	args := []string{strconv.Itoa(favQK)}
	for i := 0; i < 100; i++ {
		args = append(args, strconv.Itoa(state[i]))
	}
	return c.executeOK(commandSpecs[cmdSQK], args...)
}

func (c *Client) GetDepartmentQuickKeys(favQK, sysQK int) (QuickKeyState, error) {
	resp, err := c.execute(commandSpecs[cmdDQK], strconv.Itoa(favQK), strconv.Itoa(sysQK))
	if err != nil {
		return QuickKeyState{}, err
	}
	return parseQuickKeys(resp.Fields, 2)
}

func (c *Client) SetDepartmentQuickKeys(favQK, sysQK int, state QuickKeyState) error {
	args := []string{strconv.Itoa(favQK), strconv.Itoa(sysQK)}
	for i := 0; i < 100; i++ {
		args = append(args, strconv.Itoa(state[i]))
	}
	return c.executeOK(commandSpecs[cmdDQK], args...)
}

func (c *Client) GetServiceTypes() ([]int, error) {
	resp, err := c.execute(commandSpecs[cmdSVC])
	if err != nil {
		return nil, err
	} else if len(resp.Fields) < 47 {
		return nil, errors.New("svc response too short")
	}
	out := make([]int, 47)
	for i := 0; i < 47; i++ {
		out[i] = parseIntDefault(resp.Fields[i], 0)
	}
	return out, nil
}

func (c *Client) SetServiceTypes(values []int) error {
	if len(values) != 47 {
		return errors.New("svc values must have length 47")
	}
	args := make([]string, 0, 47)
	for _, v := range values {
		args = append(args, strconv.Itoa(v))
	}
	return c.executeOK(commandSpecs[cmdSVC], args...)
}

func (c *Client) JumpMode(mode, index string) error {
	return c.executeOK(commandSpecs[cmdJPM], mode, index)
}

func (c *Client) GetDateTime() (DateTimeStatus, error) {
	resp, err := c.execute(commandSpecs[cmdDTM])
	if err != nil {
		return DateTimeStatus{}, err
	} else if len(resp.Fields) < 8 {
		return DateTimeStatus{}, errors.New("dtm response too short")
	}
	layout := "2006-01-02 15:04:05"
	timeText := fmt.Sprintf("%s-%s-%s %s:%s:%s", resp.Fields[1], resp.Fields[2], resp.Fields[3], resp.Fields[4], resp.Fields[5], resp.Fields[6])
	ts, err := time.Parse(layout, timeText)
	if err != nil {
		return DateTimeStatus{}, err
	}
	return DateTimeStatus{
		DaylightSaving: parseIntDefault(resp.Fields[0], 0),
		Time:           ts,
		RTCStatus:      parseIntDefault(resp.Fields[7], 0),
	}, nil
}

func (c *Client) SetDateTime(daylightSaving int, t time.Time) error {
	args := []string{
		strconv.Itoa(daylightSaving),
		t.Format("2006"),
		t.Format("01"),
		t.Format("02"),
		t.Format("15"),
		t.Format("04"),
		t.Format("05"),
	}
	return c.executeOK(commandSpecs[cmdDTM], args...)
}

func (c *Client) GetLocationRange() (LocationRange, error) {
	resp, err := c.execute(commandSpecs[cmdLCR])
	if err != nil {
		return LocationRange{}, err
	} else if len(resp.Fields) < 3 {
		return LocationRange{}, errors.New("lcr response too short")
	}
	return LocationRange{Latitude: resp.Fields[0], Longitude: resp.Fields[1], Range: resp.Fields[2]}, nil
}

func (c *Client) SetLocationRange(lat, lon, rng string) error {
	return c.executeOK(commandSpecs[cmdLCR], lat, lon, rng)
}

func (c *Client) AnalyzeStart(mode string, params ...string) (CommandResponse, error) {
	return c.execute(commandSpecs[cmdAST], append([]string{mode}, params...)...)
}

func (c *Client) AnalyzePauseResume(mode string) error {
	return c.executeOK(commandSpecs[cmdAPR], mode)
}

func (c *Client) PushWaterfallFFT(fftType int, on bool) (CommandResponse, error) {
	state := "OFF"
	if on {
		state = "ON"
	}
	return c.execute(commandSpecs[cmdPWF], strconv.Itoa(fftType), state)
}

func (c *Client) GetWaterfallFFT(fftType int, on bool) ([]int, error) {
	state := "OFF"
	if on {
		state = "ON"
	}
	resp, err := c.execute(commandSpecs[cmdGWF], strconv.Itoa(fftType), state)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(resp.Fields))
	for _, f := range resp.Fields {
		out = append(out, parseIntDefault(f, 0))
	}
	return out, nil
}

func (c *Client) GetWaterfallFFTBinary(fftType int, on bool) ([]byte, error) {
	state := "OFF"
	if on {
		state = "ON"
	}
	resp, err := c.execute(CommandSpec{Command: cmdGWF, Mode: ModeBinary}, strconv.Itoa(fftType), state)
	if err != nil {
		return nil, err
	}
	if len(resp.Raw) == 0 {
		return nil, errors.New("gw2 empty payload")
	}

	data := resp.Raw
	if idx := bytes.IndexByte(data, ','); idx >= 0 && idx+1 < len(data) {
		data = data[idx+1:]
	}
	// Strip the protocol's single trailing \r terminator, but not binary content.
	if len(data) > 0 && data[len(data)-1] == '\r' {
		data = data[:len(data)-1]
	}
	if len(data) > fftDataPoints {
		data = data[:fftDataPoints]
	}
	return append([]byte(nil), data...), nil
}

func (c *Client) UserRecordStatus() (URCStatus, error) {
	resp, err := c.execute(commandSpecs[cmdURC])
	if err != nil {
		return URCStatus{}, err
	}
	if len(resp.Fields) == 0 {
		return URCStatus{}, errors.New("urc response too short")
	}
	status := URCStatus{}
	if strings.EqualFold(resp.Fields[0], "ERR") {
		status.Status = 0
		if len(resp.Fields) >= 2 {
			status.ErrorCode = resp.Fields[1]
		}
		return status, nil
	}
	status.Status = parseIntDefault(resp.Fields[0], 0)
	return status, nil
}

func (c *Client) SetUserRecord(enable bool) error {
	state := "0"
	if enable {
		state = "1"
	}
	return c.executeOK(commandSpecs[cmdURC], state)
}

func (c *Client) EnterMenu(menuID, index string) error {
	args := []string{menuID}
	if index != "" {
		args = append(args, index)
	}
	return c.executeOK(commandSpecs[cmdMNU], args...)
}

func (c *Client) MenuStatus() (XMLNode, error) {
	resp, err := c.execute(commandSpecs[cmdMSI])
	if err != nil {
		return XMLNode{}, err
	}
	return parseXMLNode(resp.XML)
}

func (c *Client) MenuSetValue(value string) error {
	return c.executeOK(commandSpecs[cmdMSV], "", strings.ReplaceAll(value, ",", "\t"))
}

func (c *Client) MenuBack(retLevel string) error {
	return c.executeOK(commandSpecs[cmdMSB], "", retLevel)
}

func (c *Client) KeepAlive() error {
	_, err := c.execute(commandSpecs[cmdKAL])
	return err
}

func (c *Client) PowerOff() error {
	return c.executeOK(commandSpecs[cmdPOF])
}

func (c *Client) GetChargeStatus() (ChargeStatus, error) {
	resp, err := c.execute(commandSpecs[cmdGCS])
	if err != nil {
		return ChargeStatus{}, err
	}
	return ParseGCSResponse(string(resp.Raw))
}

func (c *Client) GetVolume() (int, error) {
	resp, err := c.execute(commandSpecs[cmdVOL])
	if err != nil {
		return 0, err
	} else if len(resp.Fields) == 0 {
		return 0, errors.New("vol response too short")
	}
	return parseIntDefault(resp.Fields[0], 0), nil
}

func (c *Client) SetVolume(level int) error {
	if level < 0 || level > 29 {
		return errors.New("volume must be in range 0-29")
	}
	resp, err := c.execute(commandSpecs[cmdVOL], strconv.Itoa(level))
	if err != nil {
		return err
	} else if resp.IsNG() {
		return fmt.Errorf("volume set failed: %s", strings.Join(resp.Fields, ","))
	}
	return nil
}

func (c *Client) GetSquelch() (int, error) {
	resp, err := c.execute(commandSpecs[cmdSQL])
	if err != nil {
		return 0, err
	} else if len(resp.Fields) == 0 {
		return 0, errors.New("sql response too short")
	}
	return parseIntDefault(resp.Fields[0], 0), nil
}

func (c *Client) SetSquelch(level int) error {
	if level < 0 || level > 19 {
		return errors.New("squelch must be in range 0-19")
	}
	resp, err := c.execute(commandSpecs[cmdSQL], strconv.Itoa(level))
	if err != nil {
		return err
	} else if resp.IsNG() {
		return fmt.Errorf("squelch set failed: %s", strings.Join(resp.Fields, ","))
	}
	return nil
}

func requireOK(resp CommandResponse) error {
	if resp.IsOK() {
		return nil
	} else if resp.IsNG() {
		return fmt.Errorf("command %s failed: %s", resp.Command, strings.Join(resp.Fields, ","))
	} else if len(resp.Fields) == 0 {
		return fmt.Errorf("command %s: missing response", resp.Command)
	}
	return fmt.Errorf("command %s: unexpected response: %s", resp.Command, strings.Join(resp.Fields, ","))
}

func (c *Client) executeOK(spec CommandSpec, args ...string) error {
	resp, err := c.execute(spec, args...)
	if err != nil {
		return err
	}
	return requireOK(resp)
}
