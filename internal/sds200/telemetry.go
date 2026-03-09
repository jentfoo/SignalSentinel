package sds200

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DisplayLine struct {
	Text string
	Mode string
}

type StatusSTS struct {
	DisplayForm string
	Lines       []DisplayLine
	Reserved    []string
	RawFields   []string
}

type StatusGST struct {
	StatusSTS
	Mute      string
	LED1      string
	LED2      string
	WFMode    string
	Frequency string
	Mod       string
	MFPos     string
	Center    string
	Lower     string
	Upper     string
	ColorMode string
	FFTSize   string
}

type ScannerInfo struct {
	Mode      string
	VScreen   string
	Property  map[string]string
	AGC       map[string]string
	DualWatch map[string]string
	Nodes     map[string][]map[string]string
	Raw       XMLNode
}

func ParseSTS(fields []string) (StatusSTS, error) {
	if len(fields) < 1 {
		return StatusSTS{}, errors.New("sts fields empty")
	}
	lineCount := len(fields[0])
	if lineCount < 5 || lineCount > 20 {
		return StatusSTS{}, fmt.Errorf("invalid display form length: %d", lineCount)
	}
	for _, ch := range fields[0] {
		if ch != '0' && ch != '1' {
			return StatusSTS{}, errors.New("invalid display form value")
		}
	}

	expectedPairs := lineCount * 2
	if len(fields) < 1+expectedPairs {
		return StatusSTS{}, fmt.Errorf("insufficient fields for %d display lines", lineCount)
	}

	lines := make([]DisplayLine, 0, lineCount)
	for i := 0; i < expectedPairs; i += 2 {
		text := strings.TrimSpace(strings.ReplaceAll(fields[1+i], "\t", ","))
		mode := strings.TrimSpace(fields[1+i+1])
		lines = append(lines, DisplayLine{Text: text, Mode: mode})
	}

	var reserved []string
	if len(fields) > 1+expectedPairs {
		reserved = fields[1+expectedPairs:]
	}

	return StatusSTS{
		DisplayForm: fields[0],
		Lines:       lines,
		Reserved:    reserved,
		RawFields:   append([]string(nil), fields...),
	}, nil
}

// ParseGST parses GST (Get Scanner Status) response fields.
// Assumes V2.00 response format (LED1/LED2/COLOR_MODE fields).
// The spec states all SDS200 units support V2.00 commands.
// On pre-V2.00 firmware these fields contain reserved values (typically "0").
func ParseGST(fields []string) (StatusGST, error) {
	base, err := ParseSTS(fields)
	if err != nil {
		return StatusGST{}, err
	}
	lineCount := len(base.DisplayForm)
	idx := 1 + lineCount*2
	if len(fields) <= idx+11 {
		return StatusGST{}, errors.New("insufficient gst fields")
	}

	status := StatusGST{StatusSTS: base}
	status.Mute = safeField(fields, idx)
	status.LED1 = safeField(fields, idx+1)
	status.LED2 = safeField(fields, idx+2)
	status.WFMode = safeField(fields, idx+3)
	status.Frequency = safeField(fields, idx+4)
	status.Mod = safeField(fields, idx+5)
	status.MFPos = safeField(fields, idx+6)
	status.Center = safeField(fields, idx+7)
	status.Lower = safeField(fields, idx+8)
	status.Upper = safeField(fields, idx+9)
	status.ColorMode = safeField(fields, idx+10)
	status.FFTSize = safeField(fields, idx+11)
	return status, nil
}

func safeField(fields []string, idx int) string {
	if idx < 0 || idx >= len(fields) {
		return ""
	}
	return fields[idx]
}

func ParseScannerInfoXML(payload []byte) (ScannerInfo, error) {
	node, err := parseXMLNode(payload)
	if err != nil {
		return ScannerInfo{}, err
	}
	if node.XMLName.Local != "ScannerInfo" {
		return ScannerInfo{}, fmt.Errorf("unexpected root element: %s", node.XMLName.Local)
	}

	out := ScannerInfo{
		Mode:      node.Attrs["Mode"],
		VScreen:   node.Attrs["V_Screen"],
		Property:  map[string]string{},
		AGC:       map[string]string{},
		DualWatch: map[string]string{},
		Nodes:     map[string][]map[string]string{},
		Raw:       node,
	}

	for _, child := range node.Children {
		attrs := map[string]string{}
		for k, v := range child.Attrs {
			attrs[k] = v
		}

		switch child.XMLName.Local {
		case "Property":
			out.Property = attrs
		case "AGC":
			out.AGC = attrs
		case "DualWatch":
			out.DualWatch = attrs
		default:
			out.Nodes[child.XMLName.Local] = append(out.Nodes[child.XMLName.Local], attrs)
		}
	}

	return out, nil
}

type RuntimeStatus struct {
	Connected   bool
	Mode        string
	ViewScreen  string
	Frequency   string
	System      string
	Department  string
	Channel     string
	Talkgroup   string
	Hold        bool
	Signal      int
	P25Status   string
	SquelchOpen bool
	Volume      int
	Squelch     int
	Mute        bool
	HoldTarget  HoldTarget
	Avoided     bool
	AvoidKnown  bool
	ActivityAt  time.Time
	UpdatedAt   time.Time
	LastSource  string
}

// IsTransmissionActive determines whether current scanner status indicates
// voice activity that should be treated as an active transmission.
func IsTransmissionActive(status RuntimeStatus) bool {
	if !status.Connected {
		return false
	}
	if isActivityTelemetryStale(status) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(status.P25Status), "Data") {
		return false
	}
	if status.SquelchOpen {
		return true
	}
	return status.Signal > 0 && !status.Mute
}

func isActivityTelemetryStale(status RuntimeStatus) bool {
	const maxActivityStaleness = 3 * time.Second
	if status.ActivityAt.IsZero() || status.UpdatedAt.IsZero() {
		return false
	}
	return status.UpdatedAt.After(status.ActivityAt.Add(maxActivityStaleness))
}

type HoldTarget struct {
	Keyword     string
	Arg1        string
	Arg2        string
	SystemIndex string
}

type TelemetryStore struct {
	mu     sync.RWMutex
	status RuntimeStatus
}

func NewTelemetryStore() *TelemetryStore {
	return &TelemetryStore{}
}

func (s *TelemetryStore) Snapshot() RuntimeStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.status
}

func (s *TelemetryStore) UpdateFromSTS(sts StatusSTS) RuntimeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.status.UpdatedAt = time.Now()
	s.status.LastSource = cmdSTS
	if len(sts.Lines) > 0 {
		s.status.Channel = strings.TrimSpace(sts.Lines[0].Text)
	}
	return s.status
}

func (s *TelemetryStore) UpdateFromGST(gst StatusGST) RuntimeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.status.Connected = true
	s.status.UpdatedAt = now
	s.status.LastSource = cmdGST
	s.status.Frequency = strings.TrimSpace(gst.Frequency)
	s.status.Mute = strings.EqualFold(strings.TrimSpace(gst.Mute), "Mute") || strings.TrimSpace(gst.Mute) == "1"
	s.status.SquelchOpen = !s.status.Mute
	s.status.ActivityAt = now
	return s.status
}

func (s *TelemetryStore) UpdateFromScannerInfo(info ScannerInfo) RuntimeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.status.Mode = info.Mode
	s.status.ViewScreen = info.VScreen
	s.status.Connected = true
	s.status.LastSource = cmdPSI
	s.status.UpdatedAt = now

	if v, ok := info.Property["VOL"]; ok {
		s.status.Volume = parseIntDefault(v, s.status.Volume)
	}
	if v, ok := info.Property["SQL"]; ok {
		s.status.Squelch = parseIntDefault(v, s.status.Squelch)
	}
	if v, ok := info.Property["Sig"]; ok {
		s.status.Signal = parseIntDefault(v, s.status.Signal)
		s.status.ActivityAt = now
	}
	if v, ok := info.Property["Mute"]; ok {
		s.status.Mute = strings.EqualFold(v, "Mute")
		s.status.SquelchOpen = !s.status.Mute
		s.status.ActivityAt = now
	}
	if v, ok := info.Property["P25Status"]; ok {
		s.status.P25Status = strings.TrimSpace(v)
		s.status.ActivityAt = now
	}

	s.status.Hold = hasHoldState(info.Nodes)
	s.status.HoldTarget = deriveHoldTarget(info)
	s.status.Avoided, s.status.AvoidKnown = deriveAvoidState(info)

	if nodes := info.Nodes["ConvFrequency"]; len(nodes) > 0 {
		cf := nodes[0]
		s.status.Channel = cf["Name"]
		s.status.Talkgroup = ""
		s.status.Frequency = cf["Freq"]
		s.status.System = extractName(info.Nodes["System"])
		s.status.Department = extractName(info.Nodes["Department"])
	} else if nodes := info.Nodes["TGID"]; len(nodes) > 0 {
		tg := nodes[0]
		s.status.Channel = tg["Name"]
		s.status.Talkgroup = tg["TGID"]
		if freq := tg["Freq"]; freq != "" {
			s.status.Frequency = freq
		}
		s.status.System = extractName(info.Nodes["System"])
		s.status.Department = extractName(info.Nodes["Department"])
	}

	return s.status
}

func deriveAvoidState(info ScannerInfo) (bool, bool) {
	nodeOrder := []string{
		"ConvFrequency",
		"TGID",
		"SrchFrequency",
		"CcHitsChannel",
		"WxChannel",
		"ToneOutChannel",
		"Department",
		"System",
	}
	for _, key := range nodeOrder {
		nodes := info.Nodes[key]
		if len(nodes) == 0 {
			continue
		}
		avoided, known := parseAvoidValue(nodes[0]["Avoid"])
		if known {
			return avoided, true
		}
	}
	return false, false
}

func parseAvoidValue(value string) (bool, bool) {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return false, false
	}
	switch v {
	case "off", "0", "none", "no", "false":
		return false, true
	default:
		return true, true
	}
}

func deriveHoldTarget(info ScannerInfo) HoldTarget {
	if target, ok := holdTargetFromNode(info.Nodes, "ConvFrequency", "CFREQ", "Index", ""); ok {
		return target
	}
	if target, ok := holdTargetFromNode(info.Nodes, "TGID", "TGID", "TGID", "Site"); ok {
		if tgidNodes := info.Nodes["TGID"]; len(tgidNodes) > 0 {
			target.SystemIndex = strings.TrimSpace(tgidNodes[0]["SystemIndex"])
		}
		if sysNodes := info.Nodes["System"]; len(sysNodes) > 0 {
			if target.SystemIndex == "" {
				target.SystemIndex = strings.TrimSpace(sysNodes[0]["Index"])
			}
		}
		if deptNodes := info.Nodes["Department"]; len(deptNodes) > 0 {
			if target.SystemIndex == "" {
				target.SystemIndex = strings.TrimSpace(deptNodes[0]["SystemIndex"])
			}
		}
		return target
	}
	if target, ok := holdTargetFromNode(info.Nodes, "SrchFrequency", "SWS_FREQ", "Freq", "DeptIndex"); ok {
		return target
	}
	if target, ok := holdTargetFromNode(info.Nodes, "CcHitsChannel", "CCHIT", "Index", ""); ok {
		return target
	}
	if target, ok := holdTargetFromNode(info.Nodes, "WxChannel", "WX", "Index", ""); ok {
		return target
	}
	if target, ok := holdTargetFromNode(info.Nodes, "ToneOutChannel", "FTO", "Index", ""); ok {
		return target
	}
	if target, ok := holdTargetFromNode(info.Nodes, "Department", "DEPT", "Index", "SystemIndex"); ok {
		return target
	}
	if target, ok := holdTargetFromNode(info.Nodes, "System", "SYS", "Index", ""); ok {
		return target
	}
	return HoldTarget{}
}

func holdTargetFromNode(nodes map[string][]map[string]string, nodeKey, keyword, arg1Key, arg2Key string) (HoldTarget, bool) {
	values := nodes[nodeKey]
	if len(values) == 0 {
		return HoldTarget{}, false
	}
	node := values[0]
	arg1 := strings.TrimSpace(node[arg1Key])
	if arg1 == "" {
		return HoldTarget{}, false
	}
	target := HoldTarget{
		Keyword: keyword,
		Arg1:    arg1,
	}
	if arg2Key != "" {
		target.Arg2 = strings.TrimSpace(node[arg2Key])
	}
	return target, true
}

func extractName(nodes []map[string]string) string {
	if len(nodes) == 0 {
		return ""
	}
	return nodes[0]["Name"]
}

func hasHoldState(nodes map[string][]map[string]string) bool {
	holdSources := []string{
		"ConvFrequency",
		"TGID",
		"SrchFrequency",
		"CcHitsChannel",
		"ToneOutChannel",
		"WxChannel",
		"Department",
		"System",
		"Site",
	}
	for _, key := range holdSources {
		for _, node := range nodes[key] {
			if strings.EqualFold(strings.TrimSpace(node["Hold"]), "On") {
				return true
			}
		}
	}
	return false
}

type ChargeStatus struct {
	Status      int
	VoltageMV   int
	CapacityPct int
	CurrentMA   int
	TempC       float64
	Raw         string
}

func ParseGCSResponse(raw string) (ChargeStatus, error) {
	out := ChargeStatus{Raw: raw}
	trim := strings.TrimSpace(raw)
	if !strings.HasPrefix(trim, "GCS,") {
		return out, errors.New("invalid gcs response")
	}
	body := strings.TrimPrefix(trim, "GCS,")
	parts := strings.Split(body, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		switch {
		case strings.HasPrefix(p, "CST="):
			out.Status = parseIntDefault(strings.TrimPrefix(p, "CST="), out.Status)
		case strings.HasPrefix(p, "VOLT="):
			v := strings.TrimPrefix(p, "VOLT=")
			sp := strings.SplitN(v, "mV:", 2)
			if len(sp) == 2 {
				out.VoltageMV = parseIntDefault(sp[0], out.VoltageMV)
				out.CapacityPct = parseIntDefault(strings.TrimSuffix(sp[1], "%"), out.CapacityPct)
			}
		case strings.HasPrefix(p, "CURR="):
			out.CurrentMA = parseIntDefault(strings.TrimSuffix(strings.TrimPrefix(p, "CURR="), "mA"), out.CurrentMA)
		case strings.HasPrefix(p, "TEMP="):
			t := strings.TrimSuffix(strings.TrimPrefix(p, "TEMP="), "C")
			t = strings.TrimSpace(t)
			f, err := strconv.ParseFloat(t, 64)
			if err == nil {
				out.TempC = f
			}
		}
	}
	return out, nil
}
