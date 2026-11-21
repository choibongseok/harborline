//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSinkholeRecordsExternalLookupsAsPositiveControl(t *testing.T) {
	// 대조군: 같은 네트워크에서 외부 이름을 풀이하면 싱크홀이 기록해야, "0건" 이라는 결과에 의미가 있다.
	out, _ := sh(root, nil, "docker", "run", "--rm", "--network", "hl-e2e-site", "--dns", "10.77.0.2", "alpine:3.22", "nslookup", controlName)
	t.Logf("%s", out)
	logs, _ := sh(root, nil, "docker", "logs", "hl-e2e-site-sinkhole-1")
	if !strings.Contains(logs, "SINK dns name="+controlName) {
		t.Fatalf("싱크홀이 대조군 조회를 기록하지 못함:\n%s", logs)
	}
}

func TestFreshInstallRunsOfflineWithCustomerSideAdapters(t *testing.T) {
	fresh(t, "v1.0.0", nil)
	if s := readState(t); s.Version != "v1.0.0" || s.Status != "running" {
		t.Fatalf("%+v", s)
	}
	seed(t, 3)
	code, sys := api(t, "GET", "/api/system", nil)
	ad, _ := sys["adapters"].(map[string]any)
	if code != 200 || sys["profile"] != "onprem" || ad["storage"] != "disk" || ad["mail"] != "smtp" || ad["llm"] != "internal" {
		t.Fatalf("설치형 어댑터로 떠야 함: %d %v", code, sys)
	}
	mails := mailsReceived(t)
	if len(mails) != 1 || !strings.Contains(mails[0], "tls=true") {
		t.Fatalf("답변 메일이 고객 SMTP 에 TLS 로 도착해야 함: %v", mails)
	}
	if c, m := api(t, "POST", "/api/inquiries/"+listItems(t)[0].(map[string]any)["id"].(string)+"/draft", nil); c != 200 {
		t.Fatalf("내부 모델 서버로 답변 초안: %d %v", c, m)
	}
	assertNoExternalAttempts(t)
}

func TestUpgradeWithoutSchemaChangeKeepsData(t *testing.T) {
	fresh(t, "v1.0.0", nil)
	seed(t, 3)
	before := snapshot(t)
	if c, _ := api(t, "GET", "/api/stats", nil); c != 404 {
		t.Fatalf("v1.0 에는 /api/stats 가 없어야 함: %d", c)
	}
	out, code := upgrade(t, "v1.1.0")
	if code != 0 || !strings.Contains(out, "스키마 변경 없음") {
		t.Fatalf("%d %s", code, out)
	}
	if s := readState(t); s.Version != "v1.1.0" || s.Previous != "v1.0.0" || s.Schema != "none" || s.LastBackup == "" {
		t.Fatalf("%+v", s)
	}
	login(t)
	if c, m := api(t, "GET", "/api/stats", nil); c != 200 || m["open"] != float64(2) {
		t.Fatalf("v1.1 통계: %d %v", c, m)
	}
	if after := snapshot(t); after != before {
		t.Fatalf("업그레이드 뒤 데이터가 같아야 함:\n전 %s\n후 %s", before, after)
	}
	assertNoExternalAttempts(t)
}

func TestUpgradeWithExpandMigrationAddsColumnsAndKeepsData(t *testing.T) {
	fresh(t, "v1.0.0", nil)
	seed(t, 3)
	before := snapshot(t)
	out, code := upgrade(t, "v1.2.0")
	if code != 0 || !strings.Contains(out, "0002_inquiry_state.sql") {
		t.Fatalf("%d %s", code, out)
	}
	login(t)
	items := listItems(t)
	if len(items) != 3 || items[0].(map[string]any)["state"] == nil {
		t.Fatalf("v1.2 는 state 를 돌려줘야 함: %v", items)
	}
	if n := psql(t, "SELECT count(*) FROM inquiries WHERE state = 1"); n != "1" {
		t.Fatalf("백필된 답변 완료 건수 1 이어야 함: %s", n)
	}
	if after := snapshot(t); after != before {
		t.Fatalf("데이터 변동:\n전 %s\n후 %s", before, after)
	}
	assertNoExternalAttempts(t)
}

func TestFailedHealthCheckWithoutSchemaChangeRollsBackAutomatically(t *testing.T) {
	fresh(t, "v1.1.0", nil)
	seed(t, 3)
	before := snapshot(t)
	out, code := upgrade(t, "v1.1.1")
	if code != 4 || !strings.Contains(out, "이전 버전으로 복귀, 상태 점검 통과") {
		t.Fatalf("자동 복귀(종료 코드 4)여야 함: %d %s", code, out)
	}
	if s := readState(t); s.Version != "v1.1.0" || s.Status != "running" {
		t.Fatalf("%+v", s)
	}
	if _, c := ctl(t, "status"); c != 0 {
		t.Fatal("복귀한 버전이 상태 점검을 통과해야 함")
	}
	login(t)
	if c, _ := api(t, "GET", "/api/stats", nil); c != 200 {
		t.Fatalf("v1.1 이 서비스 중이어야 함: %d", c)
	}
	if after := snapshot(t); after != before {
		t.Fatalf("데이터 변동:\n전 %s\n후 %s", before, after)
	}
	assertNoExternalAttempts(t)
}

func TestMigrationFailingMidwayKeepsPreviousVersionServing(t *testing.T) {
	fresh(t, "v1.1.0", nil)
	seed(t, 2)
	out, code := upgrade(t, "v1.2.1")
	if code != 4 || !strings.Contains(out, "적용: 0003_assignee.sql") {
		t.Fatalf("0003 까지 적용되고 0004 에서 실패해 자동 복귀해야 함: %d %s", code, out)
	}
	if got := psql(t, "SELECT string_agg(file, ',' ORDER BY file) FROM schema_migrations"); got != "0001_init.sql,0002_inquiry_state.sql,0003_assignee.sql" {
		t.Fatalf("부분 적용 상태: %s", got)
	}
	// 스키마가 일부 확장된 상태에서도 이전 버전(v1.1.0)이 쓰기까지 정상이다.
	seed(t, 1)
	if s := readState(t); s.Version != "v1.1.0" {
		t.Fatalf("%+v", s)
	}
	assertNoExternalAttempts(t)
}

func TestBreakingSchemaFailureHaltsAndBackupRestoreRecovers(t *testing.T) {
	fresh(t, "v1.2.0", nil)
	seed(t, 3)
	before := snapshot(t)
	out, code := upgrade(t, "v2.0.0")
	if code != 5 || !strings.Contains(out, "harborctl restore") {
		t.Fatalf("정지(종료 코드 5)와 복구 안내여야 함: %d %s", code, out)
	}
	if s := readState(t); s.Status != "halted" || s.Version != "v1.2.0" {
		t.Fatalf("%+v", s)
	}
	if running, _ := sh(root, nil, "docker", "ps", "-q", "--filter", "name="+project+"-app"); strings.TrimSpace(running) != "" {
		t.Fatal("정지 상태에서는 앱 컨테이너가 떠 있으면 안 됨")
	}
	if o, c := upgrade(t, "v1.2.0"); c == 0 || !strings.Contains(o, "restore") {
		t.Fatalf("정지 상태에서는 다른 업그레이드를 막고 복구를 안내해야 함: %d %s", c, o)
	}
	if o, c := ctl(t, "restore", "--backup", "latest"); c != 0 {
		t.Fatalf("복구 실패: %s", o)
	}
	if s := readState(t); s.Version != "v1.2.0" || s.Status != "running" {
		t.Fatalf("%+v", s)
	}
	if after := snapshot(t); after != before {
		t.Fatalf("복구한 데이터가 업그레이드 전과 같아야 함:\n전 %s\n후 %s", before, after)
	}
	login(t)
	if n := len(listItems(t)); n != 3 {
		t.Fatalf("복구 뒤 조회 건수 %d", n)
	}
	assertNoExternalAttempts(t)
}

func untouched(t *testing.T, version string) {
	t.Helper()
	if s := readState(t); s.Version != version || s.Status != "running" {
		t.Fatalf("거부된 업그레이드가 상태를 바꿈: %+v", s)
	}
	if _, err := os.Stat(filepath.Join(instDir, "backups")); err == nil {
		es, _ := os.ReadDir(filepath.Join(instDir, "backups"))
		if len(es) != 0 {
			t.Fatalf("거부된 업그레이드는 백업도 만들지 않아야 함: %d개", len(es))
		}
	}
}

func TestBundleWithWrongSignatureIsRejectedBeforeAnyChange(t *testing.T) {
	fresh(t, "v1.2.0", nil)
	out, code := upgrade(t, "v1.2.2")
	if code != 2 || !strings.Contains(out, "서명이 맞지 않음") {
		t.Fatalf("%d %s", code, out)
	}
	untouched(t, "v1.2.0")
	assertNoExternalAttempts(t)
}

func TestBundleWithTamperedImageIsRejectedByChecksum(t *testing.T) {
	fresh(t, "v1.2.0", nil)
	out, code := upgrade(t, "v1.2.3")
	if code != 2 || !strings.Contains(out, "체크섬이 맞지 않음") {
		t.Fatalf("%d %s", code, out)
	}
	untouched(t, "v1.2.0")
}

func TestSkippedVersionIsRejectedByManifestGate(t *testing.T) {
	fresh(t, "v1.0.0", nil)
	out, code := upgrade(t, "v1.4.0")
	if code != 2 || !strings.Contains(out, "건너뛴 버전") {
		t.Fatalf("%d %s", code, out)
	}
	untouched(t, "v1.0.0")
}

func TestDowngradeThroughUpgradeIsRejected(t *testing.T) {
	fresh(t, "v1.2.0", nil)
	out, code := upgrade(t, "v1.1.0")
	if code != 2 || !strings.Contains(out, "내리는 것은 업그레이드가 아님") {
		t.Fatalf("%d %s", code, out)
	}
	untouched(t, "v1.2.0")
}

func TestManualRollbackAfterCompatibleUpgradeKeepsData(t *testing.T) {
	fresh(t, "v1.1.0", nil)
	seed(t, 2)
	if _, code := upgrade(t, "v1.2.0"); code != 0 {
		t.Fatal("v1.2.0 업그레이드")
	}
	seed(t, 1) // 새 버전에서 쓴 데이터도 롤백 뒤 남아야 한다
	before := snapshot(t)
	if out, code := ctl(t, "rollback"); code != 0 {
		t.Fatalf("롤백: %s", out)
	}
	if s := readState(t); s.Version != "v1.1.0" || s.Status != "running" {
		t.Fatalf("%+v", s)
	}
	login(t)
	if n := len(listItems(t)); n != 3 {
		t.Fatalf("롤백 뒤 건수 %d", n)
	}
	if after := snapshot(t); after != before {
		t.Fatalf("롤백 뒤 데이터 변동:\n전 %s\n후 %s", before, after)
	}
}

func TestManualRollbackIsRefusedWhenCurrentReleaseChangedSchemaIncompatibly(t *testing.T) {
	fresh(t, "v1.1.0", nil)
	if _, code := upgrade(t, "v1.2.0"); code != 0 {
		t.Fatal("v1.2.0 업그레이드")
	}
	st := filepath.Join(instDir, "state.json")
	b, _ := os.ReadFile(st)
	_ = os.WriteFile(st, []byte(strings.Replace(string(b), `"schema": "compatible"`, `"schema": "breaking"`, 1)), 0o644)
	out, code := ctl(t, "rollback")
	if code == 0 || !strings.Contains(out, "롤백할 수 없음") || !strings.Contains(out, "restore") {
		t.Fatalf("breaking 상태에서는 롤백을 거부하고 복구를 안내해야 함: %d %s", code, out)
	}
}

func TestBackupAndRestoreReturnsDataAndAttachmentsToTheSnapshot(t *testing.T) {
	fresh(t, "v1.2.0", nil)
	seed(t, 4)
	before := snapshot(t)
	out, code := ctl(t, "backup")
	if code != 0 {
		t.Fatalf("%s", out)
	}
	// 백업 뒤 데이터를 망가뜨린다: 문의 삭제 + 첨부 파일 삭제 + 새 문의
	psql(t, "DELETE FROM inquiries WHERE subject = '문의 1'")
	es, _ := os.ReadDir(filepath.Join(instDir, "data", "attachments"))
	for _, e := range es { // 폴더 자체는 컨테이너가 마운트하고 있으므로 안의 내용만 지운다
		_ = os.RemoveAll(filepath.Join(instDir, "data", "attachments", e.Name()))
	}
	seed(t, 1)
	if snapshot(t) == before {
		t.Fatal("데이터를 망가뜨리지 못함")
	}
	if o, c := ctl(t, "restore", "--backup", "latest"); c != 0 {
		t.Fatalf("복구: %s", o)
	}
	if after := snapshot(t); after != before {
		t.Fatalf("복구한 건수·체크섬이 백업 시점과 같아야 함:\n전 %s\n후 %s", before, after)
	}
	login(t)
	items := listItems(t)
	id := items[0].(map[string]any)["id"].(string)
	_, m := api(t, "GET", "/api/inquiries/"+id, nil)
	atts, _ := m["attachments"].([]any)
	if len(atts) != 1 {
		t.Fatalf("첨부 메타데이터: %v", m)
	}
	resp, err := client.Get(gateway + "/api/attachments/" + atts[0].(map[string]any)["id"].(string))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("복구된 첨부를 내려받아야 함: %v", err)
	}
	resp.Body.Close()
}

func TestRestoreRefusesTamperedBackup(t *testing.T) {
	fresh(t, "v1.2.0", nil)
	seed(t, 2)
	if _, code := ctl(t, "backup"); code != 0 {
		t.Fatal("백업")
	}
	ids, _ := os.ReadDir(filepath.Join(instDir, "backups"))
	dump := filepath.Join(instDir, "backups", ids[len(ids)-1].Name(), "db.dump")
	f, _ := os.OpenFile(dump, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.Write([]byte("x"))
	f.Close()
	before := snapshot(t)
	out, code := ctl(t, "restore", "--backup", "latest")
	if code == 0 || !strings.Contains(out, "체크섬이 맞지 않음") {
		t.Fatalf("변조된 백업은 복구하지 않아야 함: %d %s", code, out)
	}
	if snapshot(t) != before {
		t.Fatal("거부했는데 데이터가 바뀜")
	}
}

func TestDoctorPassesOnHealthyInstallIncludingPrivateCAChain(t *testing.T) {
	fresh(t, "v1.0.0", nil)
	out, code := ctl(t, "doctor")
	for _, want := range []string{"컨테이너 런타임", "디스크", "인증서 ca.pem", "DB 접속", "시각", "TLS smtp.corp.test:587"} {
		if !strings.Contains(out, want) {
			t.Errorf("점검 항목 %q 가 없음", want)
		}
	}
	if code != 0 || strings.Contains(out, "[fail]") {
		t.Fatalf("정상 설치에서는 실패 항목이 없어야 함: %d\n%s", code, out)
	}
}

func TestDoctorFailsOnExpiredCertificateAndShortDisk(t *testing.T) {
	destroy()
	t.Cleanup(destroy)
	cfg := writeConfig(t, configJSON(func(c map[string]any) {
		c["ca_files"] = []string{filepath.Join(certsDir, "expired-ca.pem")}
		c["min_disk_gb"] = 100000000
	}))
	out, code := ctl(t, "doctor", "--config", cfg)
	if code == 0 || !strings.Contains(out, "만료됨") || !strings.Contains(out, "[fail] 디스크") {
		t.Fatalf("만료된 인증서와 디스크 부족을 잡아야 함: %d\n%s", code, out)
	}
}

func TestInstallRefusesToStartWhenPreflightFails(t *testing.T) {
	destroy()
	t.Cleanup(destroy)
	cfg := writeConfig(t, configJSON(func(c map[string]any) { c["min_disk_gb"] = 100000000 }))
	out, code := ctl(t, "install", "--bundle", bundle("v1.0.0"), "--config", cfg, "--pubkey", pubKey)
	if code != 1 || !strings.Contains(out, "사전 점검에 실패") {
		t.Fatalf("%d %s", code, out)
	}
	if running, _ := sh(root, nil, "docker", "ps", "-aq", "--filter", "name="+project+"-app", "--filter", "name="+project+"-db"); strings.TrimSpace(running) != "" {
		t.Fatal("사전 점검 실패 시 컨테이너를 만들면 안 됨")
	}
}

func TestZeroExternalConnectionAttemptsRecordedAcrossAllScenarios(t *testing.T) {
	assertNoExternalAttempts(t)
	out, _ := sh(root, nil, "docker", "logs", "hl-e2e-site-sinkhole-1")
	t.Logf("싱크홀 전체 기록 줄 수(대조군 포함): %d", strings.Count(out, "SINK "))
}
