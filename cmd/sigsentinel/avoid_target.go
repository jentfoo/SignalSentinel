package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jentfoo/SignalSentinel/internal/sds200"
)

func resolveAvoidTarget(target sds200.HoldTarget) (sds200.HoldTarget, error) {
	target.Keyword = strings.ToUpper(strings.TrimSpace(target.Keyword))
	target.Arg1 = strings.TrimSpace(target.Arg1)
	target.Arg2 = strings.TrimSpace(target.Arg2)
	target.SystemIndex = strings.TrimSpace(target.SystemIndex)

	if target.Keyword == "" || target.Arg1 == "" {
		return sds200.HoldTarget{}, errors.New("avoid target unavailable for current scanner state")
	}

	switch target.Keyword {
	case "SWS_FREQ", "CS_FREQ", "QS_FREQ", "CC":
		target.Keyword = "AFREQ"
		target.Arg2 = ""
	case "AFREQ":
		target.Arg2 = ""
	case "TGID":
		if target.SystemIndex == "" {
			return sds200.HoldTarget{}, errors.New("avoid target unavailable for TGID: missing parent system index")
		}
		target.Keyword = "ATGID"
		target.Arg2 = target.SystemIndex
	case "ATGID":
		if target.Arg2 == "" {
			target.Arg2 = target.SystemIndex
		}
		if target.Arg2 == "" {
			return sds200.HoldTarget{}, errors.New("avoid target unavailable for TGID: missing parent system index")
		}
	case "DEPT":
		target.Arg2 = ""
	case "SYS", "SITE", "CFREQ", "CCHIT", "TRN_DISCOV", "CNV_DISCOV":
		// Supported as-is.
	case "WX", "FTO", "RPTR_FREQ", "SFREQ", "STGID", "IREC_FILE", "UREC", "UREC_FILE", "BAND_SCOPE":
		return sds200.HoldTarget{}, fmt.Errorf("avoid unsupported for hold target keyword %s", target.Keyword)
	default:
		return sds200.HoldTarget{}, fmt.Errorf("avoid unsupported for hold target keyword %s", target.Keyword)
	}

	return target, nil
}
