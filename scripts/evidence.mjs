// 실제 실행 결과(번들 매니페스트, e2e 터미널 기록, LLM 호환성 결과)를 docs/evidence 로 모은다. 데모 사이트가 이 파일들을 그대로 싣는다.
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const out = 'docs/evidence';
mkdirSync(out, { recursive: true });

// 1) 매니페스트
const manifests = [];
for (const d of readdirSync('dist/bundles').filter((x) => x.startsWith('harborline-v'))) {
  const m = JSON.parse(readFileSync(join('dist/bundles', d, 'manifest.json'), 'utf8'));
  const def = JSON.parse(readFileSync(`releases/${m.version}.json`, 'utf8'));
  manifests.push({ ...m, note: def.note ?? '', break: def.break ?? '', tamper: def.tamper ?? '' });
}
const vkey = (v) => v.replace('v', '').split('.').map((n) => String(n).padStart(4, '0')).join('.');
manifests.sort((a, b) => vkey(a.version).localeCompare(vkey(b.version)));
writeFileSync(join(out, 'manifests.json'), JSON.stringify(manifests, null, 2) + '\n');

// 2) e2e 터미널 기록: 테스트별로, 시나리오를 보여 주는 명령만(설치 준비용 install 은 신규 설치 시험에서만)
const lines = readFileSync('dist/e2e-transcripts.jsonl', 'utf8').split('\n').filter(Boolean).map((l) => JSON.parse(l));
const keep = {
  TestFreshInstallRunsOfflineWithCustomerSideAdapters: ['install'],
  TestUpgradeWithoutSchemaChangeKeepsData: ['upgrade'],
  TestUpgradeWithExpandMigrationAddsColumnsAndKeepsData: ['upgrade'],
  TestFailedHealthCheckWithoutSchemaChangeRollsBackAutomatically: ['upgrade'],
  TestMigrationFailingMidwayKeepsPreviousVersionServing: ['upgrade'],
  TestBreakingSchemaFailureHaltsAndBackupRestoreRecovers: ['upgrade', 'restore'],
  TestBundleWithWrongSignatureIsRejectedBeforeAnyChange: ['upgrade'],
  TestBundleWithTamperedImageIsRejectedByChecksum: ['upgrade'],
  TestSkippedVersionIsRejectedByManifestGate: ['upgrade'],
  TestDowngradeThroughUpgradeIsRejected: ['upgrade'],
  TestManualRollbackAfterCompatibleUpgradeKeepsData: ['rollback'],
  TestBackupAndRestoreReturnsDataAndAttachmentsToTheSnapshot: ['backup', 'restore'],
  TestDoctorPassesOnHealthyInstallIncludingPrivateCAChain: ['doctor'],
};
const e2e = {};
for (const [test, verbs] of Object.entries(keep)) {
  const recs = lines.filter((r) => r.test === test && verbs.includes(r.cmd.split(' ')[1]));
  if (!recs.length) throw new Error(`기록 없음: ${test}`);
  // 같은 명령이 여러 번이면(예: 정지 상태에서 거부된 재시도) 시나리오 순서대로 모두 싣는다
  e2e[test] = recs.map(({ cmd, output, exit }) => ({ cmd, output, exit }));
}
writeFileSync(join(out, 'e2e-transcripts.json'), JSON.stringify(e2e, null, 2) + '\n');
console.log(`evidence: 매니페스트 ${manifests.length}개, e2e 기록 ${Object.keys(e2e).length}개 시나리오`);
