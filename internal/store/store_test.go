package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// newTestStore returns a freshly-initialized Store backed by a temp file.
// Cleanup is registered automatically.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	return s
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

// ---------- money ----------

func TestParseAmount(t *testing.T) {
	ok := []struct {
		in   string
		want int64
	}{
		{"0", 0},
		{"1", 100},
		{"12.34", 1234},
		{"12.3", 1230},
		{"12.", 1200},
		{"-5.50", -550},
		{"+100", 10000},
		{"0.05", 5},
	}
	for _, c := range ok {
		got, err := ParseAmount(c.in)
		if err != nil {
			t.Errorf("ParseAmount(%q) unexpected err %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseAmount(%q)=%d, want %d", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "1.234", "1.2.3"} {
		if _, err := ParseAmount(bad); err == nil {
			t.Errorf("ParseAmount(%q) expected error", bad)
		}
	}
}

func TestFormatAmount(t *testing.T) {
	cases := map[int64]string{
		0:    "0.00",
		100:  "1.00",
		1234: "12.34",
		-550: "-5.50",
		5:    "0.05",
	}
	for in, want := range cases {
		if got := FormatAmount(in); got != want {
			t.Errorf("FormatAmount(%d)=%q, want %q", in, got, want)
		}
	}
}

// ---------- init ----------

func TestInitSeedsOnce(t *testing.T) {
	s := newTestStore(t)
	cats, err := s.ListCategories("", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) < 13 {
		t.Errorf("expected >=13 seed categories, got %d", len(cats))
	}
	// Re-init is idempotent.
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	cats2, _ := s.ListCategories("", "", 0)
	if len(cats2) != len(cats) {
		t.Errorf("re-init seeded extras: %d -> %d", len(cats), len(cats2))
	}
}

// v0Schema is the legacy schema (pre-account-removal). Inlined so the test
// pins what migration is supposed to consume — keeping it in sync with the
// real shipped v0 isn't necessary; what matters is that the columns the
// migration touches (account, txn.account_id, transfer kinds) are present.
const v0Schema = `
CREATE TABLE family (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    currency TEXT NOT NULL DEFAULT 'CNY',
    note TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE person (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    family_id INTEGER REFERENCES family(id) ON DELETE SET NULL,
    note TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE account (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('cash','bank','credit','investment','virtual')),
    currency TEXT NOT NULL DEFAULT 'CNY',
    initial_balance INTEGER NOT NULL DEFAULT 0,
    owner_kind TEXT NOT NULL CHECK (owner_kind IN ('person','family')),
    owner_id INTEGER NOT NULL,
    archived INTEGER NOT NULL DEFAULT 0,
    note TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE category (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('income','expense','transfer')),
    parent_id INTEGER REFERENCES category(id) ON DELETE SET NULL,
    owner_kind TEXT CHECK (owner_kind IN ('person','family')),
    owner_id INTEGER,
    icon TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE txn (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    occurred_at TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('income','expense','transfer')),
    amount INTEGER NOT NULL CHECK (amount > 0),
    currency TEXT NOT NULL DEFAULT 'CNY',
    account_id INTEGER NOT NULL REFERENCES account(id) ON DELETE RESTRICT,
    counter_account_id INTEGER REFERENCES account(id) ON DELETE RESTRICT,
    category_id INTEGER REFERENCES category(id) ON DELETE SET NULL,
    person_id INTEGER REFERENCES person(id) ON DELETE SET NULL,
    family_id INTEGER REFERENCES family(id) ON DELETE SET NULL,
    payee TEXT,
    note TEXT,
    tags TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
`

// TestMigrateV0ToV1 exercises the v0→v1 upgrade end-to-end: build a DB with
// the legacy schema, seed representative rows, run Init() (which detects the
// `account` table and runs migrate_v1.sql), and assert the contract.
func TestMigrateV0ToV1(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "v0.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if _, err := s.DB.Exec(v0Schema); err != nil {
		t.Fatalf("apply v0 schema: %v", err)
	}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO family(id, name) VALUES (1, '张家')`)
	exec(`INSERT INTO person(id, name, family_id) VALUES (1, '张三', 1)`)
	exec(`INSERT INTO person(id, name, family_id) VALUES (2, '张四', 1)`)
	// account 100 is owned by person 1; account 200 by family 1.
	exec(`INSERT INTO account(id, name, type, owner_kind, owner_id) VALUES (100, 'cash', 'cash', 'person', 1)`)
	exec(`INSERT INTO account(id, name, type, owner_kind, owner_id) VALUES (200, 'shared', 'bank', 'family', 1)`)
	exec(`INSERT INTO category(id, name, kind) VALUES (10, '餐饮', 'expense')`)
	exec(`INSERT INTO category(id, name, kind) VALUES (11, '工资', 'income')`)
	exec(`INSERT INTO category(id, name, kind) VALUES (12, '内部转账', 'transfer')`) // must be dropped
	// 4 txns covering the four migration branches:
	//   id=1: transfer  → dropped
	//   id=2: expense with explicit person_id   → preserved as-is
	//   id=3: expense via person-owned account  → person_id backfilled
	//   id=4: expense via family-owned account  → family_id backfilled
	exec(`INSERT INTO txn(id, occurred_at, kind, amount, account_id, category_id) VALUES (1, '2026-04-01T00:00:00Z', 'transfer', 5000, 100, 12)`)
	exec(`INSERT INTO txn(id, occurred_at, kind, amount, account_id, category_id, person_id) VALUES (2, '2026-04-02T00:00:00Z', 'expense', 1100, 100, 10, 2)`)
	exec(`INSERT INTO txn(id, occurred_at, kind, amount, account_id, category_id) VALUES (3, '2026-04-03T00:00:00Z', 'expense', 2200, 100, 10)`)
	exec(`INSERT INTO txn(id, occurred_at, kind, amount, account_id, category_id) VALUES (4, '2026-04-04T00:00:00Z', 'expense', 3300, 200, 10)`)

	// Run the migration.
	if err := s.Init(); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}

	// account table dropped.
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='account'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("account table not dropped (count=%d)", n)
	}

	// schema_meta bumped to v1.
	var ver string
	if err := s.DB.QueryRow(`SELECT value FROM schema_meta WHERE key='version'`).Scan(&ver); err != nil {
		t.Fatalf("schema_meta: %v", err)
	}
	if ver != "1" {
		t.Errorf("schema_meta.version=%q, want 1", ver)
	}

	// transfer category dropped, expense/income survive.
	s.DB.QueryRow(`SELECT COUNT(*) FROM category WHERE id=12`).Scan(&n)
	if n != 0 {
		t.Errorf("transfer category should be dropped")
	}
	s.DB.QueryRow(`SELECT COUNT(*) FROM category WHERE id IN (10, 11)`).Scan(&n)
	if n != 2 {
		t.Errorf("expense+income categories should survive, got %d", n)
	}

	// transfer txn dropped.
	s.DB.QueryRow(`SELECT COUNT(*) FROM txn WHERE id=1`).Scan(&n)
	if n != 0 {
		t.Errorf("transfer txn should be dropped")
	}

	check := func(id int64, wantPerson, wantFamily sql.NullInt64) {
		t.Helper()
		var per, fam sql.NullInt64
		if err := s.DB.QueryRow(`SELECT person_id, family_id FROM txn WHERE id=?`, id).Scan(&per, &fam); err != nil {
			t.Fatalf("txn %d: %v", id, err)
		}
		if per != wantPerson {
			t.Errorf("txn %d person_id=%v, want %v", id, per, wantPerson)
		}
		if fam != wantFamily {
			t.Errorf("txn %d family_id=%v, want %v", id, fam, wantFamily)
		}
	}
	nz := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
	check(2, nz(2), sql.NullInt64{}) // explicit person preserved, family NULL
	check(3, nz(1), sql.NullInt64{}) // backfilled from person-owned account 100
	check(4, sql.NullInt64{}, nz(1)) // backfilled from family-owned account 200

	// FK pragma is back ON after migration.
	var fkOn int
	if err := s.DB.QueryRow(`PRAGMA foreign_keys`).Scan(&fkOn); err != nil {
		t.Fatal(err)
	}
	if fkOn != 1 {
		t.Errorf("foreign_keys=%d after migrate, want 1", fkOn)
	}

	// Re-running Init() on a v1 DB must be a no-op (no `account` table to find).
	if err := s.Init(); err != nil {
		t.Fatalf("second Init: %v", err)
	}
}

// ---------- entities ----------

func TestFamilyPersonLink(t *testing.T) {
	s := newTestStore(t)
	fam, err := s.CreateFamily("测试家", "CNY", "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreatePerson("张三", &fam.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.FamilyID == nil || *p.FamilyID != fam.ID {
		t.Fatalf("person not linked to family: %+v", p)
	}
	ps, _ := s.ListPersons(&fam.ID)
	if len(ps) != 1 {
		t.Errorf("expected 1 family member, got %d", len(ps))
	}
}

// ---------- transaction validation ----------

func TestTxnValidation(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreatePerson("u", nil, "")

	if _, err := s.CreateTxn(Txn{Kind: "expense", Amount: 0, PersonID: &p.ID}); err == nil {
		t.Error("expected error on zero amount")
	}
	if _, err := s.CreateTxn(Txn{Kind: "expense", Amount: -1, PersonID: &p.ID}); err == nil {
		t.Error("expected error on negative amount")
	}
	if _, err := s.CreateTxn(Txn{Kind: "transfer", Amount: 100, PersonID: &p.ID}); err == nil {
		t.Error("expected error on unsupported transfer kind")
	}
}

func TestUpdateTxnCategory(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreatePerson("u", nil, "")
	food, _ := s.FindCategoryByName("餐饮", "expense")
	transit, _ := s.FindCategoryByName("交通", "expense")

	tx, err := s.CreateTxn(Txn{
		Kind: "expense", Amount: 100, PersonID: &p.ID, CategoryID: &food.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// switch to a different category
	if err := s.UpdateTxnCategory(tx.ID, &transit.ID); err != nil {
		t.Fatalf("update to transit: %v", err)
	}
	got, _ := s.GetTxn(tx.ID)
	if got.CategoryID == nil || *got.CategoryID != transit.ID {
		t.Errorf("want category=%d, got %v", transit.ID, got.CategoryID)
	}

	// clear category
	if err := s.UpdateTxnCategory(tx.ID, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, _ = s.GetTxn(tx.ID)
	if got.CategoryID != nil {
		t.Errorf("want cleared, got %v", *got.CategoryID)
	}

	// unknown id → error
	if err := s.UpdateTxnCategory(999999, &food.ID); err == nil {
		t.Error("expected error for unknown txn id")
	}
}

func TestUpdateTxnTags(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreatePerson("u", nil, "")
	when := mustDate(t, "2026-04-10")

	tx, err := s.CreateTxn(Txn{Kind: "expense", Amount: 100, PersonID: &p.ID, OccurredAt: when})
	if err != nil {
		t.Fatal(err)
	}
	if tx.Tags != "" {
		t.Fatalf("expected untagged txn, got %q", tx.Tags)
	}

	// set tags on a txn that had none
	if err := s.UpdateTxnTags(tx.ID, "旅行,新疆"); err != nil {
		t.Fatalf("set tags: %v", err)
	}
	got, _ := s.GetTxn(tx.ID)
	if got.Tags != "旅行,新疆" {
		t.Errorf("tags=%q, want 旅行,新疆", got.Tags)
	}

	// replace existing tags
	if err := s.UpdateTxnTags(tx.ID, "美食"); err != nil {
		t.Fatalf("replace tags: %v", err)
	}
	got, _ = s.GetTxn(tx.ID)
	if got.Tags != "美食" {
		t.Errorf("tags=%q, want 美食", got.Tags)
	}

	// clear via empty string
	if err := s.UpdateTxnTags(tx.ID, ""); err != nil {
		t.Fatalf("clear tags: %v", err)
	}
	got, _ = s.GetTxn(tx.ID)
	if got.Tags != "" {
		t.Errorf("tags=%q, want empty", got.Tags)
	}

	// unknown id → error (bad ids must not silently succeed)
	if err := s.UpdateTxnTags(999999, "新疆"); err == nil {
		t.Error("expected error for unknown txn id")
	}

	// Round-trip: the write path and the tag-filter read path must agree.
	if err := s.UpdateTxnTags(tx.ID, "旅行,新疆"); err != nil {
		t.Fatal(err)
	}
	found, err := s.ListTxns(TxnFilter{Tags: []string{"新疆"}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != tx.ID || found[0].Tags != "旅行,新疆" {
		t.Errorf("tag round-trip: got %+v, want the tagged txn", found)
	}

	// Whole-token semantics: a superstring must not match.
	if err := s.UpdateTxnTags(tx.ID, "新疆行"); err != nil {
		t.Fatal(err)
	}
	found, _ = s.ListTxns(TxnFilter{Tags: []string{"新疆"}, Limit: 100})
	if len(found) != 0 {
		t.Errorf("新疆行 should not match tag 新疆: %+v", found)
	}
}

// ---------- categories ----------

func TestFindCategoryByName(t *testing.T) {
	s := newTestStore(t)
	c, err := s.FindCategoryByName("餐饮", "expense")
	if err != nil || c == nil {
		t.Fatalf("want to find 餐饮, got %+v err=%v", c, err)
	}
	if c.Kind != "expense" {
		t.Errorf("kind=%s", c.Kind)
	}
	miss, err := s.FindCategoryByName("no-such-thing", "expense")
	if err != nil {
		t.Fatal(err)
	}
	if miss != nil {
		t.Errorf("unexpected hit: %+v", miss)
	}
}

func TestFindCategoryPrefersGlobal(t *testing.T) {
	s := newTestStore(t)
	// Seed category "餐饮" is global. Add a person-scoped one with the same name
	// — global should still win in `FindCategoryByName`.
	p, _ := s.CreatePerson("u", nil, "")
	pk := "person"
	_, err := s.CreateCategory(Category{
		Name: "餐饮", Kind: "expense",
		OwnerKind: &pk, OwnerID: &p.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.FindCategoryByName("餐饮", "expense")
	if c == nil || c.OwnerID != nil {
		t.Errorf("expected global category, got %+v", c)
	}
}

// ---------- filters ----------

func TestListTxnsFilters(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreatePerson("u", nil, "")
	food, _ := s.FindCategoryByName("餐饮", "expense")
	for _, d := range []string{"2026-04-05", "2026-04-15", "2026-04-25"} {
		s.CreateTxn(Txn{Kind: "expense", Amount: 1000, PersonID: &p.ID, CategoryID: &food.ID, OccurredAt: mustDate(t, d)})
	}
	s.CreateTxn(Txn{Kind: "expense", Amount: 2000, PersonID: &p.ID, CategoryID: &food.ID, OccurredAt: mustDate(t, "2026-04-10"), Note: "特殊关键词"})

	all, _ := s.ListTxns(TxnFilter{Limit: 100})
	if len(all) != 4 {
		t.Errorf("all count=%d, want 4", len(all))
	}
	mid, _ := s.ListTxns(TxnFilter{Since: "2026-04-10", Until: "2026-04-20", Limit: 100})
	if len(mid) != 2 {
		t.Errorf("mid count=%d, want 2 (10, 15)", len(mid))
	}
	sr, _ := s.ListTxns(TxnFilter{Search: "特殊", Limit: 10})
	if len(sr) != 1 {
		t.Errorf("search count=%d, want 1", len(sr))
	}
}

// ---------- tag filter ----------

func TestListTxnsTagFilter(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreatePerson("u", nil, "")
	when := mustDate(t, "2026-04-10")

	// Distinct amounts identify rows in assertions.
	mk := func(amount int64, tags string) {
		t.Helper()
		if _, err := s.CreateTxn(Txn{Kind: "expense", Amount: amount, PersonID: &p.ID, OccurredAt: when, Tags: tags}); err != nil {
			t.Fatal(err)
		}
	}
	mk(100, "新疆")          // exact single tag
	mk(200, "旅行,新疆")      // 新疆 as a later token
	mk(300, "新疆行")         // superstring — must NOT match "新疆"
	mk(400, "旅行")          // 旅行 only
	mk(500, "50%")          // tag containing a LIKE wildcard
	mk(600, "")             // untagged
	mk(700, "新疆,旅行,美食") // both tags

	amounts := func(f TxnFilter) map[int64]bool {
		t.Helper()
		f.Limit = 100
		got, err := s.ListTxns(f)
		if err != nil {
			t.Fatal(err)
		}
		m := map[int64]bool{}
		for _, tx := range got {
			m[tx.Amount] = true
		}
		return m
	}

	// Single tag exact match: only whole-token 新疆 rows (100, 200, 700 — not 300).
	got := amounts(TxnFilter{Tags: []string{"新疆"}})
	want := map[int64]bool{100: true, 200: true, 300: false, 400: false, 700: true}
	for amt, w := range want {
		if got[amt] != w {
			t.Errorf("tag 新疆: amount=%d present=%v, want %v", amt, got[amt], w)
		}
	}

	// Substring must not match: 新疆行 is a different token.
	if got[300] {
		t.Error("tag 新疆 leaked 新疆行 (substring matched)")
	}

	// Multiple tags OR together.
	got = amounts(TxnFilter{Tags: []string{"新疆", "旅行"}})
	for amt, w := range map[int64]bool{100: true, 200: true, 400: true, 700: true, 300: false, 500: false, 600: false} {
		if got[amt] != w {
			t.Errorf("tags 新疆,旅行: amount=%d present=%v, want %v", amt, got[amt], w)
		}
	}

	// No match → empty.
	got = amounts(TxnFilter{Tags: []string{"不存在"}})
	if len(got) != 0 {
		t.Errorf("unmatched tag should return empty, got %v", got)
	}

	// LIKE wildcards are literal: 50% matches only the 50% row, not 50 or 500.
	got = amounts(TxnFilter{Tags: []string{"50%"}})
	if !got[500] || len(got) != 1 {
		t.Errorf("tag 50%%: got %v, want only amount 500", got)
	}

	// Tags compose with the scope gate (AND, not OR).
	famA, _ := s.CreateFamily("A家", "CNY", "")
	famB, _ := s.CreateFamily("B家", "CNY", "")
	alice, _ := s.CreatePerson("alice", &famA.ID, "")
	carl, _ := s.CreatePerson("carl", &famB.ID, "")
	s.CreateTxn(Txn{Kind: "expense", Amount: 11, PersonID: &alice.ID, OccurredAt: when, Tags: "旅行"})
	s.CreateTxn(Txn{Kind: "expense", Amount: 22, PersonID: &carl.ID, OccurredAt: when, Tags: "旅行"})

	got = amounts(TxnFilter{Tags: []string{"旅行"}, ScopePersonID: alice.ID})
	if !got[11] {
		t.Errorf("alice should see her tagged txn: %v", got)
	}
	if got[22] {
		t.Error("scope must still hide carl's tagged txn")
	}
}

// ---------- family scope ----------

// TestScopeRestrictsToFamily exercises the security gate: scoped queries must
// expose own + sibling + family-tagged txns, and must NOT expose another
// family's data.
func TestScopeRestrictsToFamily(t *testing.T) {
	s := newTestStore(t)

	famA, _ := s.CreateFamily("A家", "CNY", "")
	famB, _ := s.CreateFamily("B家", "CNY", "")
	alice, _ := s.CreatePerson("alice", &famA.ID, "")
	bob, _ := s.CreatePerson("bob", &famA.ID, "")  // alice's family
	carl, _ := s.CreatePerson("carl", &famB.ID, "") // other family
	loner, _ := s.CreatePerson("loner", nil, "")    // no family

	when := mustDate(t, "2026-04-10")
	s.CreateTxn(Txn{Kind: "expense", Amount: 100, PersonID: &alice.ID, OccurredAt: when, Note: "alice-own"})
	s.CreateTxn(Txn{Kind: "expense", Amount: 200, PersonID: &bob.ID, OccurredAt: when, Note: "bob-sibling"})
	s.CreateTxn(Txn{Kind: "expense", Amount: 400, FamilyID: &famA.ID, OccurredAt: when, Note: "famA-shared"})
	s.CreateTxn(Txn{Kind: "expense", Amount: 800, PersonID: &carl.ID, OccurredAt: when, Note: "carl-other"})
	s.CreateTxn(Txn{Kind: "expense", Amount: 1600, FamilyID: &famB.ID, OccurredAt: when, Note: "famB-shared"})
	s.CreateTxn(Txn{Kind: "expense", Amount: 3200, PersonID: &loner.ID, OccurredAt: when, Note: "loner"})

	// Alice's scope: own + bob + famA. Should NOT see carl, famB, loner.
	got, err := s.ListTxns(TxnFilter{ScopePersonID: alice.ID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	gotAmounts := map[int64]bool{}
	for _, t := range got {
		gotAmounts[t.Amount] = true
	}
	for amt, want := range map[int64]bool{100: true, 200: true, 400: true, 800: false, 1600: false, 3200: false} {
		if gotAmounts[amt] != want {
			t.Errorf("alice scope: amount=%d visible=%v, want %v", amt, gotAmounts[amt], want)
		}
	}

	// Loner has no family — only own txns are visible (scope falls back to self).
	got, _ = s.ListTxns(TxnFilter{ScopePersonID: loner.ID, Limit: 100})
	if len(got) != 1 || got[0].Amount != 3200 {
		t.Errorf("loner scope: got %+v, want only the 3200 row", got)
	}
}

// ---------- reporting ----------

func TestSummarize(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreatePerson("u", nil, "")
	food, _ := s.FindCategoryByName("餐饮", "expense")
	salary, _ := s.FindCategoryByName("工资", "income")
	when := mustDate(t, "2026-04-15")

	s.CreateTxn(Txn{Kind: "income", Amount: 1000000, PersonID: &p.ID, CategoryID: &salary.ID, OccurredAt: when})
	s.CreateTxn(Txn{Kind: "expense", Amount: 5000, PersonID: &p.ID, CategoryID: &food.ID, OccurredAt: when})

	sum, err := s.Summarize(TxnFilter{Since: "2026-04-01", Until: "2026-04-30"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Income != 1000000 {
		t.Errorf("income=%d, want 1000000", sum.Income)
	}
	if sum.Expense != 5000 {
		t.Errorf("expense=%d, want 5000", sum.Expense)
	}
	if sum.Net != 995000 {
		t.Errorf("net=%d", sum.Net)
	}
	if sum.TxnCount != 2 {
		t.Errorf("txn_count=%d, want 2", sum.TxnCount)
	}
}

// ---------- budget ----------

func TestPeriodWindow(t *testing.T) {
	ref := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC) // Wednesday
	cases := []struct {
		period             string
		wantStart, wantEnd string
	}{
		{"monthly", "2026-04-01", "2026-04-30"},
		{"yearly", "2026-01-01", "2026-12-31"},
		{"weekly", "2026-04-13", "2026-04-19"}, // Mon..Sun of that week
	}
	for _, c := range cases {
		st, e := periodWindow(c.period, ref)
		if st != c.wantStart || e != c.wantEnd {
			t.Errorf("%s: %s..%s, want %s..%s", c.period, st, e, c.wantStart, c.wantEnd)
		}
	}
}

func TestBudgetStatus(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreatePerson("u", nil, "")
	food, _ := s.FindCategoryByName("餐饮", "expense")
	when := mustDate(t, "2026-04-10")
	s.CreateTxn(Txn{Kind: "expense", Amount: 30000, PersonID: &p.ID, CategoryID: &food.ID, OccurredAt: when})
	s.CreateTxn(Txn{Kind: "expense", Amount: 20000, PersonID: &p.ID, CategoryID: &food.ID, OccurredAt: when})
	// A March expense (outside April window) — must not count.
	s.CreateTxn(Txn{Kind: "expense", Amount: 77777, PersonID: &p.ID, CategoryID: &food.ID, OccurredAt: mustDate(t, "2026-03-25")})

	overall, err := s.CreateBudget(Budget{
		Name: "月度", Period: "monthly", Amount: 100000,
		OwnerKind: "person", OwnerID: p.ID, StartDate: "2026-01-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.BudgetStatusAt(*overall, when)
	if err != nil {
		t.Fatal(err)
	}
	if st.Spent != 50000 {
		t.Errorf("spent=%d, want 50000", st.Spent)
	}
	if st.Remaining != 50000 {
		t.Errorf("remaining=%d, want 50000", st.Remaining)
	}
	if st.Percent != 50 {
		t.Errorf("percent=%d, want 50", st.Percent)
	}
	if st.Since != "2026-04-01" || st.Until != "2026-04-30" {
		t.Errorf("window=%s..%s", st.Since, st.Until)
	}

	cat, err := s.CreateBudget(Budget{
		Name: "餐饮", Period: "monthly", Amount: 40000,
		CategoryID: &food.ID,
		OwnerKind:  "person", OwnerID: p.ID, StartDate: "2026-01-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	st2, _ := s.BudgetStatusAt(*cat, when)
	if st2.Spent != 50000 {
		t.Errorf("scoped spent=%d", st2.Spent)
	}
	if st2.Remaining != -10000 {
		t.Errorf("over-budget remaining=%d", st2.Remaining)
	}
}

// TestBudgetFamilyOwner verifies that a family-owned budget sums txns from any
// family member as well as family-tagged txns.
func TestBudgetFamilyOwner(t *testing.T) {
	s := newTestStore(t)
	fam, _ := s.CreateFamily("A家", "CNY", "")
	alice, _ := s.CreatePerson("alice", &fam.ID, "")
	bob, _ := s.CreatePerson("bob", &fam.ID, "")
	when := mustDate(t, "2026-04-10")

	s.CreateTxn(Txn{Kind: "expense", Amount: 1000, PersonID: &alice.ID, OccurredAt: when})
	s.CreateTxn(Txn{Kind: "expense", Amount: 2000, PersonID: &bob.ID, OccurredAt: when})
	s.CreateTxn(Txn{Kind: "expense", Amount: 4000, FamilyID: &fam.ID, OccurredAt: when})

	b, err := s.CreateBudget(Budget{
		Name: "全家月度", Period: "monthly", Amount: 100000,
		OwnerKind: "family", OwnerID: fam.ID, StartDate: "2026-01-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.BudgetStatusAt(*b, when)
	if err != nil {
		t.Fatal(err)
	}
	if st.Spent != 7000 {
		t.Errorf("family-owned budget spent=%d, want 7000", st.Spent)
	}
}

// TestListBudgetsScope verifies that another family's budget is invisible.
func TestListBudgetsScope(t *testing.T) {
	s := newTestStore(t)
	famA, _ := s.CreateFamily("A家", "CNY", "")
	famB, _ := s.CreateFamily("B家", "CNY", "")
	alice, _ := s.CreatePerson("alice", &famA.ID, "")
	carl, _ := s.CreatePerson("carl", &famB.ID, "")

	s.CreateBudget(Budget{Name: "alice 月度", Period: "monthly", Amount: 10000, OwnerKind: "person", OwnerID: alice.ID, StartDate: "2026-01-01"})
	s.CreateBudget(Budget{Name: "famA 月度", Period: "monthly", Amount: 10000, OwnerKind: "family", OwnerID: famA.ID, StartDate: "2026-01-01"})
	s.CreateBudget(Budget{Name: "carl 月度", Period: "monthly", Amount: 10000, OwnerKind: "person", OwnerID: carl.ID, StartDate: "2026-01-01"})
	s.CreateBudget(Budget{Name: "famB 月度", Period: "monthly", Amount: 10000, OwnerKind: "family", OwnerID: famB.ID, StartDate: "2026-01-01"})

	bs, err := s.ListBudgets("", 0, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotNames := map[string]bool{}
	for _, b := range bs {
		gotNames[b.Name] = true
	}
	if !gotNames["alice 月度"] || !gotNames["famA 月度"] {
		t.Errorf("alice scope missing own/family budget: %v", gotNames)
	}
	if gotNames["carl 月度"] || gotNames["famB 月度"] {
		t.Errorf("alice scope leaked other family's budgets: %v", gotNames)
	}
}
