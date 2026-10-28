// Package frameworkreport validates image-owned JSONL reports for new language
// runners. It never executes processes or consumes project stdout/stderr. A
// supervisor must wire a separate report channel to Parser and supply independent
// process evidence to Finish. This authenticates framework observations, not
// arbitrary program correctness. It is not an anti-forgery boundary against
// malicious Python sharing the adapter's interpreter.
//
// Adapter contract: emit one UTF-8 JSON object followed by '\n' per event, with
// exactly the fields below (field order and JSON whitespace are insignificant):
//
//	{"type":"suite_start","seq":1,"schema_version":1,"runner":"node-test-v2"}
//	{"type":"case_start","seq":2,"id":1}
//	{"type":"case_end","seq":3,"id":1,"status":"passed"}
//	{"type":"suite_end","seq":4,"started":1,"passed":1,"failed":0,"skipped":0,"expected_failed":0,"unexpected_passed":0,"discovery_errors":0}
//
// The selected runner must match the suite header; TypeScript uses NodeTestV2
// after compilation. Sequence numbers start at 1 and increase by 1. Case IDs
// start at 1 and increase by 1 per start, independent of names/nesting. Cases may
// overlap and end out of order, exactly once each. Status is passed, failed,
// skipped, expected_failed, or unexpected_passed. Only genuine completed
// assertions use passed/failed: discovery/setup placeholders belong in the
// separate discovery_errors count (bounded by MaxCases). Summary counts must
// match all observed cases. Nothing may follow its newline, including whitespace.
// Every numeric field is a nonnegative decimal integer, without sign, fraction,
// or exponent. Duplicate/unknown/missing fields, nulls, and case aliases fail.
package frameworkreport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

const (
	NodeTestV2       = "node-test-v2"
	PythonUnittestV2 = "python-unittest-v2"
	MaxReportBytes   = 2 * 1024 * 1024
	// MaxLineBytes excludes the mandatory newline; MaxReportBytes includes it.
	MaxLineBytes = 1024
	MaxCases     = 4096

	Passed               = "passed"
	TestFailed           = "test_failed"
	InfrastructureFailed = "infrastructure_failed"
	Timeout              = "timeout"

	ReasonPassed           = "passed"
	ReasonTestFailed       = "test_failed"
	ReasonInvalidReport    = "invalid_report"
	ReasonIncompleteReport = "incomplete_report"
	ReasonDeadlineExceeded = "deadline_exceeded"
	ReasonCanceled         = "canceled"
	ReasonTruncated        = "truncated"
	ReasonProcessMissing   = "process_missing"
	ReasonProcessError     = "process_error"
	ReasonExitMismatch     = "exit_mismatch"
	ReasonDiscoveryErrors  = "discovery_errors"
	ReasonUnexpectedPass   = "unexpected_passed"
	ReasonNoCompletedCases = "no_completed_cases"
)

// Result is independent supervisor evidence, never values parsed from a report.
// Exited must affirm that ExitCode was actually collected (zero alone is not
// evidence). Err is a launch/runtime error; a controlled exit uses a nil Err.
// DeadlineExceeded means the supervisor observed its execution deadline expire;
// Canceled means cancellation without that deadline. The two are exclusive.
// Each truncation flag covers the named stream, even when the report is complete.
// This type intentionally has no log text or report bytes.
type Result struct {
	Exited           bool
	ExitCode         int
	Err              error
	DeadlineExceeded bool
	Canceled         bool
	ReportTruncated  bool
	StdoutTruncated  bool
	StderrTruncated  bool
}

// Verdict contains only fixed enum-style Class/Reason strings and bounded Tests.
// Tests counts completed passed+failed cases only, never skips/expected failures.
// Invalid or incomplete reports always return Tests=0. Complete valid reports may
// retain the actual count for process, discovery, unexpected-pass, or timeout
// failures; none of those outcomes authorizes assertion repair.
type Verdict struct {
	Class  string
	Tests  int
	Reason string
}

var errInvalidReport = errors.New("invalid_framework_report")

// Parser is an io.Writer for one report channel and is not concurrency safe.
// Memory is bounded independently of input/chunk size. Do not reuse it for a
// second run. Its zero value is invalid; construct it with New.
type Parser struct {
	runner        string
	invalidReason string
	finished      bool
	started       bool
	ended         bool
	bytes         int
	line          [MaxLineBytes]byte
	lineBytes     int
	seq           int
	cases         [MaxCases]uint8 // 0 unseen, 1 active, 2 terminal
	startedCases  int
	activeCases   int
	counts        [5]int
	discovery     int
}

var _ io.Writer = (*Parser)(nil)

// New selects one supported image-owned runner. Unsupported selections create a
// permanently invalid parser; they cannot become valid through report input.
func New(runner string) *Parser {
	p := &Parser{runner: runner}
	if runner != NodeTestV2 && runner != PythonUnittestV2 {
		p.invalidate(ReasonInvalidReport)
	}
	return p
}

func (p *Parser) invalidate(reason string) {
	if p.invalidReason == "" {
		p.invalidReason = reason
	}
}

// Write validates incrementally and returns a fixed error on invalid input. n
// counts bytes consumed, including a newline whose frame failed validation.
// After any failure, subsequent writes consume zero bytes and return an error.
// No bytes beyond the report/line limits are copied or allocated. Empty writes
// add no input; any nonempty write after Finish or the summary is invalid.
func (p *Parser) Write(data []byte) (int, error) {
	if p.invalidReason != "" || (p.runner != NodeTestV2 && p.runner != PythonUnittestV2) {
		p.invalidate(ReasonInvalidReport)
		return 0, errInvalidReport
	}
	for index, c := range data {
		if p.finished || p.ended || p.bytes == MaxReportBytes {
			p.invalidate(ReasonInvalidReport)
			return index, errInvalidReport
		}
		p.bytes++
		if c == '\n' {
			if !p.consume() {
				p.invalidate(ReasonInvalidReport)
				return index + 1, errInvalidReport
			}
			p.lineBytes = 0
		} else {
			if p.lineBytes == MaxLineBytes {
				p.invalidate(ReasonInvalidReport)
				return index, errInvalidReport
			}
			p.line[p.lineBytes] = c
			p.lineBytes++
		}
	}
	return len(data), nil
}

// Finish seals the report, then binds independent process evidence. Deadline
// evidence may produce Timeout even with an incomplete report, but never a
// repair-eligible failure. Both deadline and cancellation set is invalid process
// evidence. Writes after Finish invalidate the parser, including after success.
func (p *Parser) Finish(process Result) Verdict {
	p.finished = true
	if p.runner != NodeTestV2 && p.runner != PythonUnittestV2 {
		p.invalidate(ReasonInvalidReport)
	}
	if !p.ended || p.lineBytes != 0 {
		p.invalidate(ReasonIncompleteReport)
	}
	verdict := Verdict{Class: InfrastructureFailed}
	if p.invalidReason == "" {
		verdict.Tests = p.counts[0] + p.counts[1]
	}
	switch {
	case process.DeadlineExceeded && process.Canceled:
		verdict.Reason = ReasonProcessError
	case process.DeadlineExceeded:
		verdict.Class, verdict.Reason = Timeout, ReasonDeadlineExceeded
	case p.invalidReason != "":
		verdict.Reason = p.invalidReason
	case process.Canceled:
		verdict.Reason = ReasonCanceled
	case process.ReportTruncated || process.StdoutTruncated || process.StderrTruncated:
		verdict.Reason = ReasonTruncated
	case process.Err != nil:
		verdict.Reason = ReasonProcessError
	case !process.Exited:
		verdict.Reason = ReasonProcessMissing
	case p.discovery > 0:
		verdict.Reason = ReasonDiscoveryErrors
	case p.counts[4] > 0:
		verdict.Reason = ReasonUnexpectedPass
	case verdict.Tests == 0:
		verdict.Reason = ReasonNoCompletedCases
	case process.ExitCode == 0 && p.counts[1] == 0:
		verdict.Class, verdict.Reason = Passed, ReasonPassed
	case process.ExitCode >= 1 && process.ExitCode <= 124 && p.counts[1] > 0:
		verdict.Class, verdict.Reason = TestFailed, ReasonTestFailed
	default:
		verdict.Reason = ReasonExitMismatch
	}
	return verdict
}

// fields reads a flat exact-key object without encoding/json's permissive
// struct-field matching, duplicate-key replacement, or UTF-8 replacement.
func fields(line []byte) (map[string]any, bool) {
	if !utf8.Valid(line) {
		return nil, false
	}
	d := json.NewDecoder(bytes.NewReader(line))
	d.UseNumber()
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return nil, false
	}
	values := make(map[string]any, 11)
	for d.More() {
		keyToken, err := d.Token()
		if err != nil {
			return nil, false
		}
		key, ok := keyToken.(string)
		if !ok || len(values) == 11 {
			return nil, false
		}
		if _, duplicate := values[key]; duplicate {
			return nil, false
		}
		value, err := d.Token()
		if err != nil {
			return nil, false
		}
		switch v := value.(type) {
		case string:
			values[key] = v
		case json.Number:
			// All integer domains are bounded by the maximum event count.
			// Check before multiply/add, so long numbers never overflow.
			n := 0
			for i, c := range string(v) {
				if c < '0' || c > '9' || (i > 0 && v[0] == '0') || n > (2*MaxCases+2-int(c-'0'))/10 {
					return nil, false
				}
				n = n*10 + int(c-'0')
			}
			values[key] = n
		default:
			return nil, false
		}
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') {
		return nil, false
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, false
	}
	return values, true
}

func exact(values map[string]any, keys ...string) bool {
	if len(values) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := values[key]; !ok {
			return false
		}
	}
	return true
}

func (p *Parser) consume() bool {
	v, ok := fields(p.line[:p.lineBytes])
	if !ok || v["seq"] != p.seq+1 {
		return false
	}
	p.seq++
	switch v["type"] {
	case "suite_start":
		if !exact(v, "type", "seq", "schema_version", "runner") || p.started || p.seq != 1 || v["schema_version"] != 1 || v["runner"] != p.runner {
			return false
		}
		p.started = true
	case "case_start":
		if !exact(v, "type", "seq", "id") || !p.started || p.startedCases == MaxCases || v["id"] != p.startedCases+1 {
			return false
		}
		p.cases[p.startedCases] = 1
		p.startedCases++
		p.activeCases++
	case "case_end":
		if !exact(v, "type", "seq", "id", "status") || !p.started {
			return false
		}
		id, ok := v["id"].(int)
		if !ok || id < 1 || id > p.startedCases || p.cases[id-1] != 1 {
			return false
		}
		statuses := [...]string{"passed", "failed", "skipped", "expected_failed", "unexpected_passed"}
		outcome := -1
		for index, status := range statuses {
			if v["status"] == status {
				outcome = index
				break
			}
		}
		if outcome < 0 {
			return false
		}
		p.cases[id-1] = 2
		p.activeCases--
		p.counts[outcome]++
	case "suite_end":
		if !exact(v, "type", "seq", "started", "passed", "failed", "skipped", "expected_failed", "unexpected_passed", "discovery_errors") || !p.started || p.activeCases != 0 || v["started"] != p.startedCases {
			return false
		}
		for index, status := range [...]string{"passed", "failed", "skipped", "expected_failed", "unexpected_passed"} {
			if v[status] != p.counts[index] {
				return false
			}
		}
		discovery, ok := v["discovery_errors"].(int)
		if !ok || discovery > MaxCases {
			return false
		}
		p.discovery = discovery
		p.ended = true
	default:
		return false
	}
	return true
}
