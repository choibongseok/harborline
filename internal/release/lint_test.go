package release

import (
	"strings"
	"testing"
)

func mig(file, rel, phase, extra, sql string) Migration {
	hdr := "-- release: " + rel + "\n-- phase: " + phase + "\n" + extra
	m, err := ParseMigration(file, hdr+sql)
	if err != nil {
		panic(err)
	}
	return m
}

func rules(vs []Violation) string {
	var r []string
	for _, v := range vs {
		r = append(r, v.Rule)
	}
	return strings.Join(r, ",")
}

func TestLintAcceptsAdditiveExpand(t *testing.T) {
	ms := []Migration{mig("0001_init.sql", "v1.0.0", "expand", "", "CREATE TABLE a (id int);"),
		mig("0002_add.sql", "v1.2.0", "expand", "", "ALTER TABLE a ADD COLUMN state smallint NOT NULL DEFAULT 0;\nCREATE INDEX ON a (state);")}
	if vs := Lint(ms); len(vs) != 0 {
		t.Fatalf("위반이 없어야 함: %v", vs)
	}
}

func TestLintRejectsDestructiveStatementsInExpand(t *testing.T) {
	cases := map[string]string{
		"drop-column":                  "ALTER TABLE a DROP COLUMN b;",
		"drop-table":                   "DROP TABLE a;",
		"rename":                       "ALTER TABLE a RENAME COLUMN b TO c;",
		"alter-type":                   "ALTER TABLE a ALTER COLUMN b TYPE bigint;",
		"set-not-null":                 "ALTER TABLE a ALTER COLUMN b SET NOT NULL;",
		"add-not-null-without-default": "ALTER TABLE a ADD COLUMN b int NOT NULL;",
		"truncate":                     "TRUNCATE a;",
	}
	for rule, sql := range cases {
		vs := Lint([]Migration{mig("0001_x.sql", "v1.0.0", "expand", "", sql)})
		if !strings.Contains(rules(vs), "expand-"+rule) {
			t.Errorf("%s 를 잡아야 함: %v", rule, vs)
		}
	}
}

func TestLintIgnoresDestructiveWordsInComments(t *testing.T) {
	m := mig("0001_x.sql", "v1.0.0", "expand", "", "-- DROP COLUMN 은 contract 에서\nCREATE TABLE a (id int);")
	if vs := Lint([]Migration{m}); len(vs) != 0 {
		t.Fatalf("주석 안의 낱말은 무시해야 함: %v", vs)
	}
}

func TestLintContractNeedsEarlierExpandAndUnusedSince(t *testing.T) {
	exp := mig("0002_add.sql", "v1.2.0", "expand", "", "ALTER TABLE a ADD COLUMN s int;")
	ok := mig("0003_drop.sql", "v1.4.0", "contract", "-- contract-of: 0002_add.sql\n-- unused-since: v1.3.0\n", "ALTER TABLE a DROP COLUMN old;")
	if vs := Lint([]Migration{exp, ok}); len(vs) != 0 {
		t.Fatalf("규약을 지킨 contract 는 통과: %v", vs)
	}
	noRef := mig("0003_drop.sql", "v1.4.0", "contract", "-- unused-since: v1.3.0\n", "ALTER TABLE a DROP COLUMN old;")
	if !strings.Contains(rules(Lint([]Migration{exp, noRef})), "contract-of") {
		t.Fatal("contract-of 없는 contract 는 거부")
	}
	sameRelease := mig("0003_drop.sql", "v1.2.0", "contract", "-- contract-of: 0002_add.sql\n-- unused-since: v1.1.0\n", "ALTER TABLE a DROP COLUMN old;")
	if !strings.Contains(rules(Lint([]Migration{exp, sameRelease})), "contract-release") {
		t.Fatal("expand 와 같은 릴리스의 contract 는 거부")
	}
	stillUsed := mig("0003_drop.sql", "v1.4.0", "contract", "-- contract-of: 0002_add.sql\n-- unused-since: v1.4.0\n", "ALTER TABLE a DROP COLUMN old;")
	if !strings.Contains(rules(Lint([]Migration{exp, stillUsed})), "unused-since") {
		t.Fatal("앞 릴리스가 아직 쓰는 것을 지우는 contract 는 거부")
	}
}

func TestLintRejectsReleaseOrderMismatch(t *testing.T) {
	ms := []Migration{mig("0001_a.sql", "v1.2.0", "expand", "", "CREATE TABLE a (id int);"),
		mig("0002_b.sql", "v1.0.0", "expand", "", "CREATE TABLE b (id int);")}
	if !strings.Contains(rules(Lint(ms)), "order") {
		t.Fatal("파일 번호와 릴리스 순서가 어긋나면 거부")
	}
}

func TestClassifyAndCheckDeclared(t *testing.T) {
	ms := []Migration{mig("0001_a.sql", "v1.0.0", "expand", "", "CREATE TABLE a (id int);"),
		mig("0002_b.sql", "v2.0.0", "breaking", "", "ALTER TABLE a DROP COLUMN old;")}
	if Classify(ms, "v1.1.0") != SchemaNone || Classify(ms, "v1.0.0") != SchemaCompatible || Classify(ms, "v2.0.0") != SchemaBreaking {
		t.Fatal("분류가 틀림")
	}
	if vs := CheckDeclared(SchemaCompatible, ms, "v2.0.0"); !strings.Contains(rules(vs), "schema-declared") {
		t.Fatal("breaking 마이그레이션을 compatible 로 선언하면 빌드 단계에서 걸려야 함")
	}
	if vs := CheckDeclared(SchemaBreaking, ms, "v2.0.0"); len(vs) != 0 {
		t.Fatalf("정직한 선언은 통과: %v", vs)
	}
}

func TestParseMigrationRequiresHeader(t *testing.T) {
	if _, err := ParseMigration("0001.sql", "CREATE TABLE a (id int);"); err == nil {
		t.Fatal("헤더 없는 마이그레이션은 거부")
	}
	if _, err := ParseMigration("0001.sql", "-- release: v1.0.0\n-- phase: shrink\n"); err == nil {
		t.Fatal("알 수 없는 phase 는 거부")
	}
}

func TestVersionParseAndCompare(t *testing.T) {
	a, _ := ParseVersion("v1.2.3")
	b, _ := ParseVersion("1.10.0")
	if a.Compare(b) >= 0 || a.String() != "v1.2.3" {
		t.Fatal("버전 비교/표기")
	}
	for _, bad := range []string{"", "v1", "v1.2", "v1.2.x", "v1..2"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("%q 는 오류여야 함", bad)
		}
	}
}
