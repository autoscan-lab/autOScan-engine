package domain

import (
	"regexp"
	"strconv"
	"strings"
)

type ValgrindStatus string

const (
	ValgrindStatusPass    ValgrindStatus = "pass"
	ValgrindStatusFail    ValgrindStatus = "fail"
	ValgrindStatusMissing ValgrindStatus = "missing"
)

type ValgrindResult struct {
	Status                   ValgrindStatus `json:"status"`
	ErrorSummary             int            `json:"error_summary"`
	DefinitelyLostBytes      int64          `json:"definitely_lost_bytes"`
	IndirectlyLostBytes      int64          `json:"indirectly_lost_bytes"`
	PossiblyLostBytes        int64          `json:"possibly_lost_bytes"`
	StillReachableBytes      int64          `json:"still_reachable_bytes"`
	OpenFileDescriptors      int            `json:"open_file_descriptors"`
	StandardFileDescriptors  int            `json:"standard_file_descriptors"`
	ExtraOpenFileDescriptors int            `json:"extra_open_file_descriptors"`
	Message                  string         `json:"message,omitempty"`
	Log                      string         `json:"log,omitempty"`
}

var (
	valgrindErrorSummaryPattern = regexp.MustCompile(`ERROR SUMMARY:\s+([0-9,]+)\s+errors?`)
	valgrindLeakPattern         = regexp.MustCompile(`(?m)(definitely lost|indirectly lost|possibly lost|still reachable):\s+([0-9,]+)\s+bytes`)
	valgrindFDPattern           = regexp.MustCompile(`FILE DESCRIPTORS:\s+([0-9,]+)\s+open(?:\s+\(([0-9,]+)\s+std\))?`)
)

func NewValgrindMissingResult(tool string) *ValgrindResult {
	return &ValgrindResult{
		Status:  ValgrindStatusMissing,
		Message: tool + " is not available.",
	}
}

func NewValgrindFailureResult(message string) *ValgrindResult {
	return &ValgrindResult{
		Status:  ValgrindStatusFail,
		Message: message,
	}
}

func ParseValgrindLog(log string) *ValgrindResult {
	result := &ValgrindResult{
		Status: ValgrindStatusPass,
		Log:    strings.TrimSpace(log),
	}

	if match := valgrindErrorSummaryPattern.FindStringSubmatch(log); len(match) == 2 {
		result.ErrorSummary = int(parseValgrindNumber(match[1]))
	}

	for _, match := range valgrindLeakPattern.FindAllStringSubmatch(log, -1) {
		if len(match) != 3 {
			continue
		}
		bytes := parseValgrindNumber(match[2])
		switch match[1] {
		case "definitely lost":
			result.DefinitelyLostBytes = bytes
		case "indirectly lost":
			result.IndirectlyLostBytes = bytes
		case "possibly lost":
			result.PossiblyLostBytes = bytes
		case "still reachable":
			result.StillReachableBytes = bytes
		}
	}

	if match := valgrindFDPattern.FindStringSubmatch(log); len(match) >= 2 {
		result.OpenFileDescriptors = int(parseValgrindNumber(match[1]))
		if len(match) >= 3 && match[2] != "" {
			result.StandardFileDescriptors = int(parseValgrindNumber(match[2]))
		} else {
			result.StandardFileDescriptors = 3
		}
		if result.OpenFileDescriptors > result.StandardFileDescriptors {
			result.ExtraOpenFileDescriptors = result.OpenFileDescriptors - result.StandardFileDescriptors
		}
	}

	if result.Fails() {
		result.Status = ValgrindStatusFail
	}

	return result
}

func (r *ValgrindResult) Fails() bool {
	if r == nil {
		return false
	}
	if r.Status == ValgrindStatusMissing {
		return true
	}
	if r.ErrorSummary > 0 {
		return true
	}
	if r.DefinitelyLostBytes > 0 ||
		r.IndirectlyLostBytes > 0 ||
		r.PossiblyLostBytes > 0 ||
		r.StillReachableBytes > 0 {
		return true
	}
	return r.ExtraOpenFileDescriptors > 0
}

func parseValgrindNumber(value string) int64 {
	cleaned := strings.ReplaceAll(value, ",", "")
	number, err := strconv.ParseInt(cleaned, 10, 64)
	if err != nil {
		return 0
	}
	return number
}
