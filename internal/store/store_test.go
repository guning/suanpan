package store

import (
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
	if len(cats) < 14 {
		t.Errorf("expected >=14 seed categories, got %d", len(cats))
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

func TestAccountValidatesOwner(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateAccount(Account{
		Name: "orphan", Type: "cash", OwnerKind: "person", OwnerID: 999,
	})
	if err == nil {
		t.Fatal("expected error creating account for nonexistent owner")
	}
}

// ---------- balances ----------

func TestAccountBalance(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreatePerson("u", nil, "")
	a, _ := s.CreateAccount(Account{
		Name: "招行", Type: "bank", Currency: "CNY",
		InitialBalance: 100000, OwnerKind: "person", OwnerID: p.ID,
	})
	b, _ := s.CreateAccount(Account{
		Name: "现金", Type: "cash", Currency: "CNY",
		OwnerKind: "person", OwnerID: p.ID,
	})
	if _, err := s.CreateTxn(Txn{Kind: "income", Amount: 50000, AccountID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTxn(Txn{Kind: "expense", Amount: 20000, AccountID: a.ID}); err != nil {
		t.Fatal(err)
	}
	to := b.ID
	if _, err := s.CreateTxn(Txn{Kind: "transfer", Amount: 30000, AccountID: a.ID, CounterAccountID: &to}); err != nil {
		t.Fatal(err)
	}
	balA, _ := s.AccountBalanceOf(a.ID)
	balB, _ := s.AccountBalanceOf(b.ID)
	if wantA := int64(100000 + 50000 - 20000 - 30000); balA != wantA {
		t.Errorf("balA=%d, want %d", balA, wantA)
	}
	if balB != 30000 {
		t.Errorf("balB=%d, want 30000", balB)
	}
}

// ---------- transaction validation ----------

func TestTransferValidation(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreatePerson("u", nil, "")
	a, _ := s.CreateAccount(Account{Name: "A", Type: "cash", OwnerKind: "person", OwnerID: p.ID})

	if _, err := s.CreateTxn(Txn{Kind: "transfer", Amount: 100, AccountID: a.ID}); err == nil {
		t.Error("expected error on transfer without counter")
	}
	same := a.ID
	if _, err := s.CreateTxn(Txn{Kind: "transfer", Amount: 100, AccountID: a.ID, CounterAccountID: &same}); err == nil {
		t.Error("expected error on same-account transfer")
	}
	b, _ := s.CreateAccount(Account{Name: "B", Type: "cash", OwnerKind: "person", OwnerID: p.ID})
	bid := b.ID
	if _, err := s.CreateTxn(Txn{Kind: "transfer", Amount: 0, AccountID: a.ID, CounterAccountID: &bid}); err == nil {
		t.Error("expected error on zero amount")
	}
	if _, err := s.CreateTxn(Txn{Kind: "expense", Amount: -1, AccountID: a.ID}); err == nil {
		t.Error("expected error on negative amount")
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
	a, _ := s.CreateAccount(Account{Name: "A", Type: "cash", OwnerKind: "person", OwnerID: p.ID})
	food, _ := s.FindCategoryByName("餐饮", "expense")
	for _, d := range []string{"2026-04-05", "2026-04-15", "2026-04-25"} {
		s.CreateTxn(Txn{Kind: "expense", Amount: 1000, AccountID: a.ID, CategoryID: &food.ID, OccurredAt: mustDate(t, d)})
	}
	s.CreateTxn(Txn{Kind: "expense", Amount: 2000, AccountID: a.ID, CategoryID: &food.ID, OccurredAt: mustDate(t, "2026-04-10"), Note: "特殊关键词"})

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

// ---------- reporting ----------

func TestSummarizeExcludesTransfers(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreatePerson("u", nil, "")
	a, _ := s.CreateAccount(Account{Name: "A", Type: "cash", OwnerKind: "person", OwnerID: p.ID})
	b, _ := s.CreateAccount(Account{Name: "B", Type: "cash", OwnerKind: "person", OwnerID: p.ID})
	food, _ := s.FindCategoryByName("餐饮", "expense")
	salary, _ := s.FindCategoryByName("工资", "income")
	when := mustDate(t, "2026-04-15")

	s.CreateTxn(Txn{Kind: "income", Amount: 1000000, AccountID: a.ID, CategoryID: &salary.ID, OccurredAt: when})
	s.CreateTxn(Txn{Kind: "expense", Amount: 5000, AccountID: a.ID, CategoryID: &food.ID, OccurredAt: when})
	bid := b.ID
	s.CreateTxn(Txn{Kind: "transfer", Amount: 30000, AccountID: a.ID, CounterAccountID: &bid, OccurredAt: when})

	sum, err := s.Summarize(TxnFilter{Since: "2026-04-01", Until: "2026-04-30"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Income != 1000000 {
		t.Errorf("income=%d, want 1000000", sum.Income)
	}
	if sum.Expense != 5000 {
		t.Errorf("expense=%d, want 5000 (transfer excluded)", sum.Expense)
	}
	if sum.Net != 995000 {
		t.Errorf("net=%d", sum.Net)
	}
	if sum.TxnCount != 3 {
		t.Errorf("txn_count=%d, want 3", sum.TxnCount)
	}
}

// ---------- budget ----------

func TestPeriodWindow(t *testing.T) {
	ref := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC) // Wednesday
	cases := []struct {
		period           string
		wantStart, wantEnd string
	}{
		{"monthly", "2026-04-01", "2026-04-30"},
		{"yearly", "2026-01-01", "2026-12-31"},
		{"weekly", "2026-04-13", "2026-04-19"}, // Mon..Sun of that week
	}
	for _, c := range cases {
		s, e := periodWindow(c.period, ref)
		if s != c.wantStart || e != c.wantEnd {
			t.Errorf("%s: %s..%s, want %s..%s", c.period, s, e, c.wantStart, c.wantEnd)
		}
	}
}

func TestBudgetStatus(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreatePerson("u", nil, "")
	a, _ := s.CreateAccount(Account{Name: "A", Type: "cash", OwnerKind: "person", OwnerID: p.ID})
	food, _ := s.FindCategoryByName("餐饮", "expense")
	when := mustDate(t, "2026-04-10")
	s.CreateTxn(Txn{Kind: "expense", Amount: 30000, AccountID: a.ID, CategoryID: &food.ID, OccurredAt: when})
	s.CreateTxn(Txn{Kind: "expense", Amount: 20000, AccountID: a.ID, CategoryID: &food.ID, OccurredAt: when})
	// A March expense (outside April window) — must not count.
	s.CreateTxn(Txn{Kind: "expense", Amount: 77777, AccountID: a.ID, CategoryID: &food.ID, OccurredAt: mustDate(t, "2026-03-25")})

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

// ---------- UpdateCategory ----------

func TestUpdateCategoryRename(t *testing.T) {
	s := newTestStore(t)
	c, _ := s.FindCategoryByName("餐饮", "expense")
	updated, err := s.UpdateCategory(c.ID, "美食", "", "", nil, false)
	if err != nil {
		t.Fatalf("UpdateCategory: %v", err)
	}
	if updated.Name != "美食" {
		t.Errorf("name=%q, want 美食", updated.Name)
	}
	if updated.Kind != "expense" {
		t.Errorf("kind=%q, want expense", updated.Kind)
	}
}

func TestUpdateCategoryIcon(t *testing.T) {
	s := newTestStore(t)
	c, _ := s.FindCategoryByName("交通", "expense")
	updated, err := s.UpdateCategory(c.ID, "", "", "🚌", nil, false)
	if err != nil {
		t.Fatalf("UpdateCategory: %v", err)
	}
	if updated.Icon != "🚌" {
		t.Errorf("icon=%q, want 🚌", updated.Icon)
	}
	if updated.Name != "交通" {
		t.Errorf("name changed to %q", updated.Name)
	}
}

func TestUpdateCategoryKind(t *testing.T) {
	s := newTestStore(t)
	c, _ := s.FindCategoryByName("餐饮", "expense")
	updated, err := s.UpdateCategory(c.ID, "", "income", "", nil, false)
	if err != nil {
		t.Fatalf("UpdateCategory: %v", err)
	}
	if updated.Kind != "income" {
		t.Errorf("kind=%q, want income", updated.Kind)
	}
}

func TestUpdateCategoryNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.UpdateCategory(9999, "不存在", "", "", nil, false)
	if err == nil {
		t.Fatal("expected error for nonexistent category")
	}
}

func TestUpdateCategoryAllFields(t *testing.T) {
	s := newTestStore(t)
	c, _ := s.FindCategoryByName("娱乐", "expense")
	updated, err := s.UpdateCategory(c.ID, "游戏", "income", "🎮", nil, false)
	if err != nil {
		t.Fatalf("UpdateCategory: %v", err)
	}
	if updated.Name != "游戏" || updated.Kind != "income" || updated.Icon != "🎮" {
		t.Errorf("got %+v", updated)
	}
}

func TestUpdateCategoryClearParent(t *testing.T) {
	s := newTestStore(t)
	// Create a parent category and a child.
	parent, err := s.CreateCategory(Category{Name: "父分类", Kind: "expense"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateCategory(Category{Name: "子分类", Kind: "expense", ParentID: &parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentID == nil || *child.ParentID != parent.ID {
		t.Fatal("parent not set")
	}
	// Clear parent.
	updated, err := s.UpdateCategory(child.ID, "", "", "", nil, true)
	if err != nil {
		t.Fatalf("UpdateCategory: %v", err)
	}
	if updated.ParentID != nil {
		t.Errorf("expected nil parent, got %d", *updated.ParentID)
	}
}

// ---------- UpdateTxnCategory ----------

func TestUpdateTxnCategoryOnly(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreatePerson("u", nil, "")
	a, _ := s.CreateAccount(Account{Name: "A", Type: "cash", OwnerKind: "person", OwnerID: p.ID})
	txn, _ := s.CreateTxn(Txn{Kind: "expense", Amount: 1000, AccountID: a.ID})
	if txn.CategoryID != nil {
		t.Fatal("expected nil category initially")
	}
	food, _ := s.FindCategoryByName("餐饮", "expense")
	updated, err := s.UpdateTxnCategory(txn.ID, &food.ID)
	if err != nil {
		t.Fatalf("UpdateTxnCategory: %v", err)
	}
	if updated.CategoryID == nil || *updated.CategoryID != food.ID {
		t.Errorf("category_id=%v, want %d", updated.CategoryID, food.ID)
	}
}

func TestUpdateTxnCategoryClear(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreatePerson("u", nil, "")
	a, _ := s.CreateAccount(Account{Name: "A", Type: "cash", OwnerKind: "person", OwnerID: p.ID})
	food, _ := s.FindCategoryByName("餐饮", "expense")
	txn, _ := s.CreateTxn(Txn{Kind: "expense", Amount: 1000, AccountID: a.ID, CategoryID: &food.ID})
	if txn.CategoryID == nil {
		t.Fatal("expected category initially")
	}
	updated, err := s.UpdateTxnCategory(txn.ID, nil)
	if err != nil {
		t.Fatalf("UpdateTxnCategory: %v", err)
	}
	if updated.CategoryID != nil {
		t.Errorf("expected cleared category, got %v", *updated.CategoryID)
	}
}

func TestUpdateTxnCategorySwap(t *testing.T) {
	s := newTestStore(t)
	p, _ := s.CreatePerson("u", nil, "")
	a, _ := s.CreateAccount(Account{Name: "A", Type: "cash", OwnerKind: "person", OwnerID: p.ID})
	food, _ := s.FindCategoryByName("餐饮", "expense")
	shop, _ := s.FindCategoryByName("购物", "expense")
	txn, _ := s.CreateTxn(Txn{Kind: "expense", Amount: 1000, AccountID: a.ID, CategoryID: &food.ID})
	updated, err := s.UpdateTxnCategory(txn.ID, &shop.ID)
	if err != nil {
		t.Fatalf("UpdateTxnCategory: %v", err)
	}
	if updated.CategoryID == nil || *updated.CategoryID != shop.ID {
		t.Errorf("category_id=%v, want %d", updated.CategoryID, shop.ID)
	}
}
