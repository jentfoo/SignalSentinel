package sds200

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultControlPort = 50536
	DefaultRTSPPort    = 554
	DefaultFTPPort     = 21

	// fftDataPoints is the number of binary data points in a GW2 FFT response.
	fftDataPoints = 240
)

const (
	cmdMDL = "MDL"
	cmdVER = "VER"
	cmdKEY = "KEY"
	cmdQSH = "QSH"
	cmdSTS = "STS"
	cmdJNT = "JNT"
	cmdNXT = "NXT"
	cmdPRV = "PRV"
	cmdFQK = "FQK"
	cmdSQK = "SQK"
	cmdDQK = "DQK"
	cmdPSI = "PSI"
	cmdGSI = "GSI"
	cmdGLT = "GLT"
	cmdHLD = "HLD"
	cmdAVD = "AVD"
	cmdSVC = "SVC"
	cmdJPM = "JPM"
	cmdDTM = "DTM"
	cmdLCR = "LCR"
	cmdAST = "AST"
	cmdAPR = "APR"
	cmdURC = "URC"
	cmdMNU = "MNU"
	cmdMSI = "MSI"
	cmdMSV = "MSV"
	cmdMSB = "MSB"
	cmdGST = "GST"
	cmdPWF = "PWF"
	cmdGWF = "GWF"
	cmdGW2 = "GW2"
	cmdKAL = "KAL"
	cmdPOF = "POF"
	cmdGCS = "GCS"
	cmdVOL = "VOL"
	cmdSQL = "SQL"
)

var (
	footerTagPattern = regexp.MustCompile(`<Footer\b[^>]*\/?>`)
	footerNoPattern  = regexp.MustCompile(`\bNo="(\d+)"`)
	footerEOTPattern = regexp.MustCompile(`\bEOT="([01])"`)
)

func buildCommand(cmd string, args ...string) []byte {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, strings.ToUpper(strings.TrimSpace(cmd)))
	for _, arg := range args {
		parts = append(parts, strings.TrimSpace(arg))
	}
	return []byte(strings.Join(parts, ",") + "\r")
}

func parseDelimitedFields(raw []byte) (cmd string, fields []string) {
	clean := strings.TrimRight(strings.TrimSpace(string(raw)), "\r\n")
	if clean == "" {
		return "", nil
	}
	parts := strings.Split(clean, ",")
	if len(parts) == 0 {
		return "", nil
	}
	return strings.ToUpper(strings.TrimSpace(parts[0])), parts[1:]
}

type xmlFragment struct {
	RootOpenTag string
	RootName    string
	Body        string
	Seq         int
	EOT         bool
}

func parseXMLFragment(raw []byte) (*xmlFragment, error) {
	text := strings.TrimRight(strings.TrimSpace(string(raw)), "\r\n")
	if text == "" {
		return nil, errors.New("empty fragment")
	}
	idx := strings.Index(text, ",<XML>,")
	if idx < 0 {
		return nil, errors.New("not an xml fragment")
	}
	x := text[idx+len(",<XML>,"):]
	x = strings.ReplaceAll(x, "\r", "")
	x = strings.ReplaceAll(x, "\n", "")
	x = strings.TrimSpace(x)

	var seq int
	var eot bool

	footerTag := footerTagPattern.FindString(x)
	if footerTag != "" {
		noMatch := footerNoPattern.FindStringSubmatch(footerTag)
		eotMatch := footerEOTPattern.FindStringSubmatch(footerTag)
		if len(noMatch) != 2 || len(eotMatch) != 2 {
			return nil, errors.New("xml footer missing attributes")
		}

		var err error
		seq, err = strconv.Atoi(noMatch[1])
		if err != nil {
			return nil, fmt.Errorf("invalid footer sequence: %w", err)
		}
		eot = eotMatch[1] == "1"
		x = strings.ReplaceAll(x, footerTag, "")
	} else {
		// Single-packet responses may omit the Footer tag entirely.
		// Verify the XML is self-contained (has a closing root tag).
		xmlBody := strings.TrimPrefix(strings.TrimSpace(x), `<?xml version="1.0" encoding="utf-8"?>`)
		xmlBody = strings.TrimSpace(xmlBody)
		if start := strings.Index(xmlBody, "<"); start >= 0 {
			end := strings.Index(xmlBody[start:], ">")
			if end >= 0 {
				rootName := xmlBody[start+1 : start+end]
				if i := strings.IndexAny(rootName, " >/"); i >= 0 {
					rootName = rootName[:i]
				}
				if !strings.Contains(xmlBody, "</"+rootName+">") {
					return nil, errors.New("xml footer missing")
				}
			}
		}
		seq = 1
		eot = true
	}

	x = strings.TrimSpace(x)
	x = strings.TrimPrefix(x, `<?xml version="1.0" encoding="utf-8"?>`)
	x = strings.TrimSpace(x)

	start := strings.Index(x, "<")
	if start < 0 {
		return nil, errors.New("xml root missing")
	}
	end := strings.Index(x[start:], ">")
	if end < 0 {
		return nil, errors.New("xml root malformed")
	}
	end += start
	rootOpen := x[start : end+1]
	rootName := rootOpen[1:]
	if i := strings.IndexAny(rootName, " >/"); i >= 0 {
		rootName = rootName[:i]
	}
	body := x[end+1:]
	closeTag := "</" + rootName + ">"
	body = strings.ReplaceAll(body, closeTag, "")

	return &xmlFragment{
		RootOpenTag: rootOpen,
		RootName:    rootName,
		Body:        body,
		Seq:         seq,
		EOT:         eot,
	}, nil
}

type xmlReassembler struct {
	rootOpen string
	rootName string
	parts    map[int]string
	lastSeq  int
	hasEOT   bool
}

func newXMLReassembler() *xmlReassembler {
	return &xmlReassembler{parts: map[int]string{}}
}

func (x *xmlReassembler) Add(fragment *xmlFragment) {
	if x.rootOpen == "" {
		x.rootOpen = fragment.RootOpenTag
		x.rootName = fragment.RootName
	}
	x.parts[fragment.Seq] = fragment.Body
	if fragment.EOT {
		x.hasEOT = true
		x.lastSeq = fragment.Seq
	}
}

func (x *xmlReassembler) Complete() bool {
	if !x.hasEOT || x.lastSeq <= 0 {
		return false
	}
	for i := 1; i <= x.lastSeq; i++ {
		if _, ok := x.parts[i]; !ok {
			return false
		}
	}
	return true
}

func (x *xmlReassembler) MissingSequences() []int {
	if !x.hasEOT || x.lastSeq <= 0 {
		return nil
	}
	missing := make([]int, 0)
	for i := 1; i <= x.lastSeq; i++ {
		if _, ok := x.parts[i]; !ok {
			missing = append(missing, i)
		}
	}
	return missing
}

func (x *xmlReassembler) Bytes() []byte {
	if !x.Complete() {
		return nil
	}
	keys := make([]int, 0, len(x.parts))
	for k := range x.parts {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
	if x.rootOpen == "" {
		x.rootOpen = "<root>"
		x.rootName = "root"
	}
	b.WriteString(x.rootOpen)
	for _, k := range keys {
		b.WriteString(x.parts[k])
	}
	b.WriteString("</")
	b.WriteString(x.rootName)
	b.WriteString(">")
	return b.Bytes()
}

type XMLNode struct {
	XMLName  xml.Name
	Attrs    map[string]string
	Content  string
	Children []XMLNode
}

func (n *XMLNode) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	n.XMLName = start.Name
	n.Attrs = make(map[string]string, len(start.Attr))
	for _, attr := range start.Attr {
		n.Attrs[attr.Name.Local] = attr.Value
	}

	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			child := XMLNode{}
			if err := dec.DecodeElement(&child, &t); err != nil {
				return err
			}
			n.Children = append(n.Children, child)
		case xml.CharData:
			if text := strings.TrimSpace(string(t)); text != "" {
				if n.Content == "" {
					n.Content = text
				} else {
					n.Content += " " + text
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
	return nil
}

func parseXMLNode(payload []byte) (XMLNode, error) {
	var node XMLNode
	if err := xml.Unmarshal(payload, &node); err != nil {
		return XMLNode{}, err
	}
	return node, nil
}

func nodeFirstChildByName(node XMLNode, local string) (XMLNode, bool) {
	for _, child := range node.Children {
		if child.XMLName.Local == local {
			return child, true
		}
	}
	return XMLNode{}, false
}

func parseIntDefault(v string, fallback int) int {
	i, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return i
}

func parseFloatDefault(v string, fallback float64) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return fallback
	}
	return f
}

func parseBoolOnOff(v string) bool {
	s := strings.TrimSpace(strings.ToLower(v))
	return s == "on" || s == "1" || s == "true"
}

func parseTimeLayouts(value string, layouts ...string) (time.Time, error) {
	for _, layout := range layouts {
		t, err := time.Parse(layout, value)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("no matching time format for %q", value)
}
