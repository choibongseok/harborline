package sim

import (
	"encoding/json"
	"strings"
	"testing"
)

func run(t *testing.T, req string) UpgradeResponse {
	t.Helper()
	out, err := Upgrade(req)
	if err != nil {
		t.Fatal(err)
	}
	var r UpgradeResponse
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

const m12 = `{"name":"harborline","version":"v1.2.0","min_from":"v1.0.0","schema":"compatible","files":[]}`

func TestSimulationReturnsTheSameDecisionsAsTheReleasePackage(t *testing.T) {
	ok := run(t, `{"current":"v1.1.0","manifest":`+m12+`,"inject":{}}`)
	if ok.Outcome != "success" || ok.CurrentAfter != "v1.2.0" || !ok.RollbackOK {
		t.Fatalf("%+v", ok)
	}
	rb := run(t, `{"current":"v1.1.0","manifest":`+m12+`,"inject":{"failAt":"health"}}`)
	if rb.Outcome != "rolled_back" || rb.CurrentAfter != "v1.1.0" {
		t.Fatalf("%+v", rb)
	}
	halt := run(t, `{"current":"v1.2.0","manifest":{"version":"v2.0.0","min_from":"v1.2.0","schema":"breaking"},"inject":{"failAt":"migrate"}}`)
	if halt.Outcome != "halted" || halt.RollbackOK || !strings.Contains(halt.Advice, "restore") {
		t.Fatalf("%+v", halt)
	}
}

func TestSimulationReportsGateAndVerificationRejections(t *testing.T) {
	skip := run(t, `{"current":"v1.0.0","manifest":{"version":"v1.4.0","min_from":"v1.3.0","schema":"none"},"inject":{}}`)
	if skip.Outcome != "rejected" || skip.Gate.Code != "below-min-from" {
		t.Fatalf("%+v", skip)
	}
	sig := run(t, `{"current":"v1.0.0","manifest":`+m12+`,"inject":{"badSignature":true}}`)
	if sig.Outcome != "rejected" || len(sig.Events) != 1 {
		t.Fatalf("%+v", sig)
	}
	sum := run(t, `{"current":"v1.0.0","manifest":`+m12+`,"inject":{"badChecksum":true}}`)
	if sum.Outcome != "rejected" || !strings.Contains(sum.Events[0].Detail, "체크섬") {
		t.Fatalf("%+v", sum)
	}
}

func TestSimulationRejectsMalformedRequests(t *testing.T) {
	if _, err := Upgrade(`{`); err == nil {
		t.Fatal("깨진 JSON 은 오류")
	}
	if _, err := Upgrade(`{"manifest":{"version":"latest"}}`); err == nil {
		t.Fatal("버전 형식 오류")
	}
}

func TestLintEntryPointFindsViolationsAndClassifies(t *testing.T) {
	out, err := Lint(`{"files":[{"name":"0001_a.sql","sql":"-- release: v1.1.0\n-- phase: expand\nALTER TABLE a DROP COLUMN b;"}],"release":"v1.1.0","declared":"compatible"}`)
	if err != nil {
		t.Fatal(err)
	}
	var r LintResponse
	_ = json.Unmarshal([]byte(out), &r)
	if len(r.Violations) == 0 || r.Violations[0].Rule != "expand-drop-column" || r.Classified != "compatible" {
		t.Fatalf("%+v", r)
	}
	out, _ = Lint(`{"files":[{"name":"x.sql","sql":"SELECT 1"}]}`)
	_ = json.Unmarshal([]byte(out), &r)
	if r.ParseError == "" {
		t.Fatal("헤더 없는 파일은 parseError 로 알려야 함")
	}
}
