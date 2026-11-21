package release

import "testing"

func mf(version, min string, s Schema) Manifest {
	return Manifest{Name: "harborline", Version: version, MinFrom: min, Schema: s}
}

func TestGateAllowsFreshInstall(t *testing.T) {
	g := Gate("", mf("v1.0.0", "v0.0.0", SchemaCompatible))
	if !g.Allowed || g.Code != GateOK {
		t.Fatalf("신규 설치는 허용되어야 함: %+v", g)
	}
}

func TestGateAllowsUpgradeAtMinFrom(t *testing.T) {
	g := Gate("v1.1.0", mf("v1.2.0", "v1.1.0", SchemaCompatible))
	if !g.Allowed {
		t.Fatalf("min_from 과 같은 버전에서는 올릴 수 있어야 함: %+v", g)
	}
}

func TestGateRejectsSkippedVersion(t *testing.T) {
	g := Gate("v1.0.0", mf("v1.4.0", "v1.3.0", SchemaNone))
	if g.Allowed || g.Code != GateSkipped {
		t.Fatalf("min_from 보다 낮으면 거부해야 함: %+v", g)
	}
}

func TestGateRejectsSameAndDowngrade(t *testing.T) {
	if g := Gate("v1.2.0", mf("v1.2.0", "v1.0.0", SchemaNone)); g.Allowed || g.Code != GateSame {
		t.Fatalf("같은 버전: %+v", g)
	}
	if g := Gate("v1.2.0", mf("v1.1.0", "v1.0.0", SchemaNone)); g.Allowed || g.Code != GateDowngrade {
		t.Fatalf("낮은 버전: %+v", g)
	}
}

func TestGateComparesNumerically(t *testing.T) {
	// 문자열 비교면 v1.10.0 < v1.9.0 으로 오판한다.
	g := Gate("v1.9.0", mf("v1.10.0", "v1.9.0", SchemaNone))
	if !g.Allowed {
		t.Fatalf("v1.9.0 → v1.10.0 은 허용되어야 함: %+v", g)
	}
}

func TestGateRejectsMalformedVersion(t *testing.T) {
	if g := Gate("1.x", mf("v1.1.0", "v1.0.0", SchemaNone)); g.Allowed || g.Code != GateBadVersion {
		t.Fatalf("현재 버전 오류: %+v", g)
	}
	if g := Gate("v1.0.0", mf("latest", "v1.0.0", SchemaNone)); g.Allowed || g.Code != GateBadVersion {
		t.Fatalf("대상 버전 오류: %+v", g)
	}
}

func TestCanRollbackBySchemaKind(t *testing.T) {
	for _, s := range []Schema{SchemaNone, SchemaCompatible} {
		if ok, _ := CanRollback(s); !ok {
			t.Fatalf("%s 는 롤백 가능해야 함", s)
		}
	}
	if ok, _ := CanRollback(SchemaBreaking); ok {
		t.Fatal("breaking 은 자동 롤백 불가여야 함")
	}
}
