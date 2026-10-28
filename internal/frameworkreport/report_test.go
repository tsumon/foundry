package frameworkreport

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

const header = `{"type":"suite_start","seq":1,"schema_version":1,"runner":"node-test-v2"}` + "\n"
const start = `{"type":"case_start","seq":2,"id":1}` + "\n"
const pass = `{"type":"case_end","seq":3,"id":1,"status":"passed"}` + "\n"
const summary = `{"type":"suite_end","seq":4,"started":1,"passed":1,"failed":0,"skipped":0,"expected_failed":0,"unexpected_passed":0,"discovery_errors":0}` + "\n"
const passing = header + start + pass + summary

func exited(code int) Result { return Result{Exited: true, ExitCode: code} }

func parse(t *testing.T, runner, report string, chunk int, process Result) Verdict {
	t.Helper()
	p := New(runner)
	for offset := 0; offset < len(report); offset += chunk {
		end := min(offset+chunk, len(report))
		_, _ = p.Write([]byte(report[offset:end]))
	}
	return p.Finish(process)
}

func assertVerdict(t *testing.T, got Verdict, class string, tests int) {
	t.Helper()
	if got.Class != class || got.Tests != tests {
		t.Fatalf("got %+v; want class %s, tests %d", got, class, tests)
	}
	if got.Reason == "" || len(got.Reason) > 64 || strings.ContainsAny(got.Reason, " \n{}") {
		t.Fatalf("reason must be a fixed bounded identifier: %q", got.Reason)
	}
	switch got.Reason {
	case ReasonPassed, ReasonTestFailed, ReasonInvalidReport, ReasonIncompleteReport,
		ReasonDeadlineExceeded, ReasonCanceled, ReasonTruncated, ReasonProcessMissing,
		ReasonProcessError, ReasonExitMismatch, ReasonDiscoveryErrors,
		ReasonUnexpectedPass, ReasonNoCompletedCases:
	default:
		t.Fatalf("reason outside the fixed diagnostic vocabulary: %q", got.Reason)
	}
}

func TestPassingReportsAcrossEveryChunkBoundary(t *testing.T) {
	for _, runner := range []string{NodeTestV2, PythonUnittestV2} {
		report := strings.ReplaceAll(passing, "node-test-v2", runner)
		for chunk := 1; chunk <= len(report)+1; chunk++ {
			assertVerdict(t, parse(t, runner, report, chunk, exited(0)), Passed, 1)
		}
		for split := 0; split <= len(report); split++ {
			p := New(runner)
			if _, err := io.WriteString(p, report[:split]); err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(p, report[split:]); err != nil {
				t.Fatal(err)
			}
			assertVerdict(t, p.Finish(exited(0)), Passed, 1)
		}
	}
}

func TestRejectsMalformedSchemaAndFrames(t *testing.T) {
	cases := map[string]string{
		"empty":                      "",
		"unknown runner":             strings.Replace(passing, "node-test-v2", "mocha", 1),
		"mismatched runner":          strings.Replace(passing, "node-test-v2", "python-unittest-v2", 1),
		"unknown key":                strings.Replace(passing, `"schema_version":1`, `"schema_version":1,"extra":0`, 1),
		"missing key":                strings.Replace(passing, `,"schema_version":1`, "", 1),
		"duplicate key":              strings.Replace(passing, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		"escaped duplicate key":      strings.Replace(passing, `"schema_version":1`, `"schema_version":1,"schema_versio\u006e":1`, 1),
		"case alias":                 strings.Replace(passing, `"runner"`, `"Runner"`, 1),
		"case alias plus exact":      strings.Replace(passing, `"schema_version":1`, `"schema_version":1,"Schema_version":1`, 1),
		"null":                       strings.Replace(passing, `"schema_version":1`, `"schema_version":null`, 1),
		"null status":                strings.Replace(passing, `"status":"passed"`, `"status":null`, 1),
		"fraction":                   strings.Replace(passing, `"schema_version":1`, `"schema_version":1.0`, 1),
		"exponent":                   strings.Replace(passing, `"schema_version":1`, `"schema_version":1e0`, 1),
		"negative zero":              strings.Replace(passing, `"failed":0`, `"failed":-0`, 1),
		"negative":                   strings.Replace(passing, `"failed":0`, `"failed":-1`, 1),
		"leading zero":               strings.Replace(passing, `"schema_version":1`, `"schema_version":01`, 1),
		"string number":              strings.Replace(passing, `"schema_version":1`, `"schema_version":"1"`, 1),
		"boolean number":             strings.Replace(passing, `"schema_version":1`, `"schema_version":true`, 1),
		"overflow":                   strings.Replace(passing, `"schema_version":1`, `"schema_version":18446744073709551616`, 1),
		"nested object":              strings.Replace(passing, `"runner":"node-test-v2"`, `"runner":{}`, 1),
		"nested array":               strings.Replace(passing, `"runner":"node-test-v2"`, `"runner":[]`, 1),
		"array root":                 "[]\n" + passing,
		"trailing json":              strings.Replace(passing, "}\n", "}{}\n", 1),
		"trailing text":              strings.Replace(passing, "}\n", "}garbage\n", 1),
		"blank first line":           "\n" + passing,
		"blank middle line":          header + " \t\n" + start + pass + summary,
		"invalid utf8":               strings.Replace(passing, "node-test-v2", "node-test-v2\xff", 1),
		"invalid utf8 whitespace":    "\xff" + passing,
		"missing final newline":      strings.TrimSuffix(passing, "\n"),
		"after summary newline":      passing + "\n",
		"after summary space":        passing + " ",
		"after summary valid event":  passing + header,
		"after summary next case":    passing + `{"type":"case_start","seq":5,"id":2}` + "\n",
		"after summary next summary": passing + strings.Replace(summary, `"seq":4`, `"seq":5`, 1),
		"summary duplicate key":      strings.Replace(passing, `"failed":0`, `"failed":0,"failed":0`, 1),
		"end unknown field":          strings.Replace(passing, `"status":"passed"`, `"status":"passed","name":"secret"`, 1),
		"unknown event":              strings.Replace(passing, `"case_start"`, `"log"`, 1),
		"unknown outcome":            strings.Replace(passing, `"passed"`, `"error"`, 1),
		"schema version":             strings.Replace(passing, `"schema_version":1`, `"schema_version":2`, 1),
	}
	for name, report := range cases {
		t.Run(name, func(t *testing.T) {
			for _, chunk := range []int{1, 17, len(report) + 1} {
				assertVerdict(t, parse(t, NodeTestV2, report, chunk, exited(0)), InfrastructureFailed, 0)
			}
		})
	}
	assertVerdict(t, parse(t, "unsupported", passing, 1, exited(0)), InfrastructureFailed, 0)
}

func TestExactFieldsOnEveryEvent(t *testing.T) {
	frames := strings.Split(strings.TrimSuffix(passing, "\n"), "\n")
	// Dropping, duplicating, or changing the case of any field must fail,
	// even if encoding/json would otherwise silently accept it.
	for index, frame := range frames {
		for _, field := range strings.Split(strings.Trim(frame, "{}"), ",") {
			key := strings.SplitN(field, ":", 2)[0]
			mutations := []string{
				strings.Replace(frame, field, field+","+field, 1),
				strings.Replace(frame, key, strings.ToUpper(key), 1),
				strings.Replace(frame, field, key+":null", 1),
				strings.Replace(frame, field+",", "", 1),
			}
			if !strings.Contains(frame, field+",") {
				mutations[3] = strings.Replace(frame, ","+field, "", 1)
			}
			for mutation, changed := range mutations {
				t.Run(fmt.Sprintf("frame_%d_%s_%d", index, key, mutation), func(t *testing.T) {
					copyFrames := append([]string(nil), frames...)
					copyFrames[index] = changed
					assertVerdict(t, parse(t, NodeTestV2, strings.Join(copyFrames, "\n")+"\n", 7, exited(0)), InfrastructureFailed, 0)
				})
			}
		}
	}
}

func TestLifecycleSequenceAndCounts(t *testing.T) {
	cases := map[string]string{
		"case before suite":       start + header + pass + summary,
		"end before suite":        pass + header + start + summary,
		"duplicate suite":         header + header + start + pass + summary,
		"duplicate summary":       passing + summary,
		"sequence starts at zero": strings.Replace(passing, `"seq":1`, `"seq":0`, 1),
		"sequence gap":            strings.Replace(passing, `"seq":2`, `"seq":3`, 1),
		"sequence duplicate":      strings.Replace(passing, `"seq":3`, `"seq":2`, 1),
		"id starts at two":        strings.ReplaceAll(passing, `"id":1`, `"id":2`),
		"unstarted end":           strings.Replace(passing, `"id":1,"status"`, `"id":2,"status"`, 1),
		"zero id":                 strings.ReplaceAll(passing, `"id":1`, `"id":0`),
		"duplicate start":         header + start + `{"type":"case_start","seq":3,"id":1}` + "\n",
		"duplicate end":           header + start + pass + `{"type":"case_end","seq":4,"id":1,"status":"passed"}` + "\n",
		"active at summary":       header + start + strings.Replace(summary, `"seq":4`, `"seq":3`, 1),
		"summary missing":         header + start + pass,
		"failed partial":          header + start + strings.Replace(pass, "passed", "failed", 1),
		"header only":             header,
		"count mismatch":          strings.Replace(passing, `"passed":1`, `"passed":0`, 1),
		"started mismatch":        strings.Replace(passing, `"started":1`, `"started":2`, 1),
		"skipped mismatch":        strings.Replace(passing, `"skipped":0`, `"skipped":1`, 1),
		"expected mismatch":       strings.Replace(passing, `"expected_failed":0`, `"expected_failed":1`, 1),
		"unexpected mismatch":     strings.Replace(passing, `"unexpected_passed":0`, `"unexpected_passed":1`, 1),
	}
	for name, report := range cases {
		t.Run(name, func(t *testing.T) {
			assertVerdict(t, parse(t, NodeTestV2, report, 1, exited(1)), InfrastructureFailed, 0)
		})
	}
}

func TestConcurrentNestedIdentitiesAndAllOutcomes(t *testing.T) {
	report := header +
		`{"type":"case_start","seq":2,"id":1}` + "\n" +
		`{"type":"case_start","seq":3,"id":2}` + "\n" +
		`{"type":"case_end","seq":4,"id":2,"status":"failed"}` + "\n" +
		`{"type":"case_start","seq":5,"id":3}` + "\n" +
		`{"type":"case_end","seq":6,"id":1,"status":"passed"}` + "\n" +
		`{"type":"case_end","seq":7,"id":3,"status":"skipped"}` + "\n" +
		`{"type":"case_start","seq":8,"id":4}` + "\n" +
		`{"type":"case_end","seq":9,"id":4,"status":"expected_failed"}` + "\n" +
		`{"type":"suite_end","seq":10,"started":4,"passed":1,"failed":1,"skipped":1,"expected_failed":1,"unexpected_passed":0,"discovery_errors":0}` + "\n"
	assertVerdict(t, parse(t, NodeTestV2, report, 1, exited(1)), TestFailed, 2)
	unexpected := strings.Replace(report, `"status":"expected_failed"`, `"status":"unexpected_passed"`, 1)
	unexpected = strings.Replace(unexpected, `"expected_failed":1,"unexpected_passed":0`, `"expected_failed":0,"unexpected_passed":1`, 1)
	assertVerdict(t, parse(t, NodeTestV2, unexpected, 3, exited(1)), InfrastructureFailed, 2)
	// Repeated/nested framework names never enter the protocol: distinct
	// harness-owned IDs are the identities. Completion may be out of order.
	for _, outcome := range []string{"passed", "failed", "skipped", "expected_failed", "unexpected_passed"} {
		t.Run(outcome, func(t *testing.T) {
			end := strings.Replace(pass, "passed", outcome, 1)
			counts := `"passed":0,"failed":0,"skipped":0,"expected_failed":0,"unexpected_passed":0`
			counts = strings.Replace(counts, `"`+outcome+`":0`, `"`+outcome+`":1`, 1)
			sum := `{"type":"suite_end","seq":4,"started":1,` + counts + `,"discovery_errors":0}` + "\n"
			class, tests, code := InfrastructureFailed, 0, 0
			if outcome == "passed" {
				class, tests = Passed, 1
			}
			if outcome == "failed" {
				class, tests, code = TestFailed, 1, 1
			}
			assertVerdict(t, parse(t, NodeTestV2, header+start+end+sum, 2, exited(code)), class, tests)
		})
	}
}

func TestDiscoveryAndNoCompletedAssertions(t *testing.T) {
	empty := header + `{"type":"suite_end","seq":2,"started":0,"passed":0,"failed":0,"skipped":0,"expected_failed":0,"unexpected_passed":0,"discovery_errors":0}` + "\n"
	assertVerdict(t, parse(t, NodeTestV2, empty, 1, exited(0)), InfrastructureFailed, 0)
	assertVerdict(t, parse(t, NodeTestV2, strings.Replace(empty, `"discovery_errors":0`, `"discovery_errors":1`, 1), 1, exited(1)), InfrastructureFailed, 0)
	assertVerdict(t, parse(t, NodeTestV2, strings.Replace(passing, `"discovery_errors":0`, `"discovery_errors":1`, 1), 1, exited(1)), InfrastructureFailed, 1)
	failed := header + start + strings.Replace(pass, "passed", "failed", 1) + strings.Replace(strings.Replace(summary, `"passed":1`, `"passed":0`, 1), `"failed":0`, `"failed":1`, 1)
	assertVerdict(t, parse(t, NodeTestV2, strings.Replace(failed, `"discovery_errors":0`, `"discovery_errors":1`, 1), 1, exited(1)), InfrastructureFailed, 1)
	assertVerdict(t, parse(t, NodeTestV2, strings.Replace(passing, `"discovery_errors":0`, `"discovery_errors":4097`, 1), 1, exited(1)), InfrastructureFailed, 0)
	unexpected := header + start + strings.Replace(pass, "passed", "unexpected_passed", 1) + strings.Replace(strings.Replace(summary, `"passed":1`, `"passed":0`, 1), `"unexpected_passed":0`, `"unexpected_passed":1`, 1)
	assertVerdict(t, parse(t, NodeTestV2, unexpected, 1, exited(1)), InfrastructureFailed, 0)
}

func TestIndependentProcessEvidence(t *testing.T) {
	failed := header + start + strings.Replace(pass, "passed", "failed", 1) + strings.Replace(strings.Replace(summary, `"passed":1`, `"passed":0`, 1), `"failed":0`, `"failed":1`, 1)
	cases := []struct {
		name                 string
		process              Result
		passClass, failClass string
	}{
		{"missing", Result{}, InfrastructureFailed, InfrastructureFailed},
		{"exit without collection", Result{ExitCode: 1}, InfrastructureFailed, InfrastructureFailed},
		{"exit zero", exited(0), Passed, InfrastructureFailed},
		{"exit one", exited(1), InfrastructureFailed, TestFailed},
		{"exit 124", exited(124), InfrastructureFailed, TestFailed},
		{"exit 125", exited(125), InfrastructureFailed, InfrastructureFailed},
		{"signal", exited(-1), InfrastructureFailed, InfrastructureFailed},
		{"signal shell code", exited(137), InfrastructureFailed, InfrastructureFailed},
		{"invalid exit", exited(256), InfrastructureFailed, InfrastructureFailed},
		{"process error", Result{Exited: true, ExitCode: 1, Err: errors.New("SECRET raw launch error")}, InfrastructureFailed, InfrastructureFailed},
		{"deadline", Result{DeadlineExceeded: true}, Timeout, Timeout},
		{"cancel", Result{Canceled: true}, InfrastructureFailed, InfrastructureFailed},
		{"contradictory cancellation evidence", Result{DeadlineExceeded: true, Canceled: true}, InfrastructureFailed, InfrastructureFailed},
		{"report truncated", Result{Exited: true, ReportTruncated: true}, InfrastructureFailed, InfrastructureFailed},
		{"stdout truncated", Result{Exited: true, StdoutTruncated: true}, InfrastructureFailed, InfrastructureFailed},
		{"stderr truncated", Result{Exited: true, StderrTruncated: true}, InfrastructureFailed, InfrastructureFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertVerdict(t, parse(t, NodeTestV2, passing, 11, tc.process), tc.passClass, 1)
			assertVerdict(t, parse(t, NodeTestV2, failed, 13, tc.process), tc.failClass, 1)
		})
	}
	assertVerdict(t, parse(t, NodeTestV2, "invalid SECRET", 1, Result{DeadlineExceeded: true}), Timeout, 0)
	// stdout/stderr are not accepted by the parser's API. A supervisor that
	// receives forged JSON on those streams still has an empty report writer.
	p := New(NodeTestV2)
	var ordinaryLogs strings.Builder
	_, _ = io.WriteString(&ordinaryLogs, passing)
	assertVerdict(t, p.Finish(exited(0)), InfrastructureFailed, 0)
}

func TestInvalidityIsStickyAndFinishSealsInput(t *testing.T) {
	p := New(NodeTestV2)
	if _, err := io.WriteString(p, "garbage\n"); err == nil {
		t.Fatal("invalid frame accepted")
	}
	if n, err := io.WriteString(p, passing); err == nil || n != 0 {
		t.Fatalf("invalid parser resumed: n=%d err=%v", n, err)
	}
	assertVerdict(t, p.Finish(exited(0)), InfrastructureFailed, 0)
	p = New(NodeTestV2)
	_, _ = io.WriteString(p, passing)
	assertVerdict(t, p.Finish(exited(0)), Passed, 1)
	if _, err := p.Write([]byte(" ")); err == nil {
		t.Fatal("write after Finish accepted")
	}
	assertVerdict(t, p.Finish(exited(0)), InfrastructureFailed, 0)
	p = New(NodeTestV2)
	_, _ = io.WriteString(p, header+start+pass)
	assertVerdict(t, p.Finish(exited(1)), InfrastructureFailed, 0)
	if _, err := io.WriteString(p, summary); err == nil {
		t.Fatal("incomplete Finish repaired by late data")
	}
	assertVerdict(t, p.Finish(exited(0)), InfrastructureFailed, 0)
}

func manyCases(count, targetBytes int) string {
	frames := []string{strings.TrimSuffix(header, "\n")}
	for id := 1; id <= count; id++ {
		frames = append(frames, fmt.Sprintf(`{"type":"case_start","seq":%d,"id":%d}`, 2*id, id))
		frames = append(frames, fmt.Sprintf(`{"type":"case_end","seq":%d,"id":%d,"status":"passed"}`, 2*id+1, id))
	}
	frames = append(frames, fmt.Sprintf(`{"type":"suite_end","seq":%d,"started":%d,"passed":%d,"failed":0,"skipped":0,"expected_failed":0,"unexpected_passed":0,"discovery_errors":0}`, 2*count+2, count, count))
	length := len(strings.Join(frames, "\n")) + 1
	for index := range frames {
		padding := min(max(targetBytes-length, 0), MaxLineBytes-len(frames[index]))
		frames[index] = strings.Repeat(" ", padding) + frames[index]
		length += padding
	}
	return strings.Join(frames, "\n") + "\n"
}

func TestIncrementalBounds(t *testing.T) {
	maxCases := manyCases(MaxCases, 0)
	assertVerdict(t, parse(t, NodeTestV2, maxCases, 19, exited(0)), Passed, 4096)
	assertVerdict(t, parse(t, NodeTestV2, manyCases(MaxCases+1, 0), 17, exited(0)), InfrastructureFailed, 0)
	lineAtLimit := strings.Repeat(" ", MaxLineBytes-len(strings.TrimSuffix(header, "\n"))) + passing
	assertVerdict(t, parse(t, NodeTestV2, lineAtLimit, 1, exited(0)), Passed, 1)
	assertVerdict(t, parse(t, NodeTestV2, " "+lineAtLimit, 1, exited(0)), InfrastructureFailed, 0)
	atLimit := manyCases(MaxCases, MaxReportBytes)
	if len(atLimit) != MaxReportBytes {
		t.Fatalf("fixture length %d", len(atLimit))
	}
	assertVerdict(t, parse(t, NodeTestV2, atLimit, 127, exited(0)), Passed, 4096)
	// This remains within the line/case limits; its last mandatory newline
	// crosses the total byte limit. The parser must reject at that byte.
	overLimit := manyCases(MaxCases, MaxReportBytes+1)
	if len(overLimit) != MaxReportBytes+1 {
		t.Fatalf("fixture length %d", len(overLimit))
	}
	p := New(NodeTestV2)
	if n, err := io.WriteString(p, overLimit[:MaxReportBytes]); err != nil || n != MaxReportBytes {
		t.Fatalf("rejected before byte limit: n=%d err=%v", n, err)
	}
	if n, err := io.WriteString(p, overLimit[MaxReportBytes:]); err == nil || n != 0 {
		t.Fatalf("accepted byte over limit: n=%d err=%v", n, err)
	}
	assertVerdict(t, p.Finish(exited(0)), InfrastructureFailed, 0)
}

func FuzzChunkInvariant(f *testing.F) {
	for _, report := range []string{passing, "", header, passing + " ", strings.Replace(passing, `"id":1`, `"id":null`, 1), "\xff\n"} {
		f.Add([]byte(report), uint16(1))
	}
	f.Fuzz(func(t *testing.T, report []byte, size uint16) {
		if len(report) > MaxReportBytes+1 {
			t.Skip()
		}
		whole := parse(t, NodeTestV2, string(report), len(report)+1, exited(0))
		chunked := parse(t, NodeTestV2, string(report), int(size)%2048+1, exited(0))
		if whole != chunked {
			t.Fatalf("chunk-dependent verdict: %+v vs %+v", whole, chunked)
		}
		if whole.Class == Passed && (whole.Tests < 1 || whole.Tests > MaxCases) {
			t.Fatalf("unbounded passing verdict: %+v", whole)
		}
	})
}
