package store

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

//go:embed migrate_v1.sql
var migrateV1SQL string

// Store wraps the SQLite handle and exposes high-level operations used by
// both the CLI and MCP server.
type Store struct {
	DB   *sql.DB
	Path string
}

// DefaultPath returns the default DB location: ~/.suanpan/suanpan.db, honoring $SUANPAN_DB.
func DefaultPath() string {
	if p := os.Getenv("SUANPAN_DB"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".suanpan", "suanpan.db")
}

// Open opens (creating the file if necessary) the SQLite DB at path.
// Callers should Close() the returned Store.
func Open(path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite writers serialize anyway; avoids "database is locked" with WAL+checkpoints.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db, Path: path}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

// Init applies the schema, runs any pending migrations, and seeds default
// categories if none exist. Safe to call repeatedly.
func (s *Store) Init() error {
	if err := s.migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if _, err := s.DB.Exec(schemaSQL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return s.seedDefaults()
}

// migrate detects legacy v0 schemas (with the `account` table) and upgrades
// them to v1 in a single transaction. New DBs are a no-op here — schema.sql
// will create the v1 tables.
func (s *Store) migrate() error {
	var hasAccount int
	if err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='account'`,
	).Scan(&hasAccount); err != nil {
		return err
	}
	if hasAccount == 0 {
		return nil // either fresh DB or already v1
	}

	// FK toggle must live outside the transaction.
	if _, err := s.DB.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	commitErr := func() error {
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
			return err
		}
		// Split & execute so we get a useful error per statement.
		for _, stmt := range splitSQL(migrateV1SQL) {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("v0→v1: %s: %w", firstLine(stmt), err)
			}
		}
		return tx.Commit()
	}()
	if _, err := s.DB.Exec(`PRAGMA foreign_keys = ON`); err != nil && commitErr == nil {
		return err
	}
	return commitErr
}

// splitSQL splits a multi-statement SQL blob on `;` at end-of-line, ignoring
// blank lines and line comments. Good enough for our migration files (no
// triggers, no quoted semicolons).
func splitSQL(s string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(s, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "--") {
			continue
		}
		cur.WriteString(line)
		cur.WriteByte('\n')
		if strings.HasSuffix(trim, ";") {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
	}
	if rest := strings.TrimSpace(cur.String()); rest != "" {
		out = append(out, rest)
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (s *Store) seedDefaults() error {
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM category WHERE owner_id IS NULL`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	seeds := []struct {
		name, kind, icon string
	}{
		{"餐饮", "expense", "🍜"},
		{"交通", "expense", "🚗"},
		{"购物", "expense", "🛍️"},
		{"居住", "expense", "🏠"},
		{"娱乐", "expense", "🎮"},
		{"医疗", "expense", "🏥"},
		{"教育", "expense", "📚"},
		{"通讯", "expense", "📱"},
		{"其他支出", "expense", "💸"},
		{"工资", "income", "💰"},
		{"奖金", "income", "🎁"},
		{"投资收益", "income", "📈"},
		{"其他收入", "income", "💵"},
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO category(name, kind, icon) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, c := range seeds {
		if _, err := stmt.Exec(c.name, c.kind, c.icon); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---------- Family ----------

func (s *Store) CreateFamily(name, currency, note string) (*Family, error) {
	if currency == "" {
		currency = "CNY"
	}
	res, err := s.DB.Exec(`INSERT INTO family(name, currency, note) VALUES(?,?,?)`, name, currency, nullIfEmpty(note))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetFamily(id)
}

func (s *Store) GetFamily(id int64) (*Family, error) {
	var f Family
	var note sql.NullString
	var created string
	err := s.DB.QueryRow(`SELECT id, name, currency, note, created_at FROM family WHERE id=?`, id).
		Scan(&f.ID, &f.Name, &f.Currency, &note, &created)
	if err != nil {
		return nil, err
	}
	f.Note = note.String
	f.CreatedAt = parseTime(created)
	return &f, nil
}

func (s *Store) ListFamilies() ([]Family, error) {
	rows, err := s.DB.Query(`SELECT id, name, currency, COALESCE(note,''), created_at FROM family ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Family
	for rows.Next() {
		var f Family
		var created string
		if err := rows.Scan(&f.ID, &f.Name, &f.Currency, &f.Note, &created); err != nil {
			return nil, err
		}
		f.CreatedAt = parseTime(created)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) DeleteFamily(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM family WHERE id=?`, id)
	return err
}

// ---------- Person ----------

func (s *Store) CreatePerson(name string, familyID *int64, note string) (*Person, error) {
	res, err := s.DB.Exec(`INSERT INTO person(name, family_id, note) VALUES(?,?,?)`,
		name, nullableInt(familyID), nullIfEmpty(note))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetPerson(id)
}

func (s *Store) GetPerson(id int64) (*Person, error) {
	var p Person
	var fid sql.NullInt64
	var note sql.NullString
	var created string
	err := s.DB.QueryRow(`SELECT id, name, family_id, note, created_at FROM person WHERE id=?`, id).
		Scan(&p.ID, &p.Name, &fid, &note, &created)
	if err != nil {
		return nil, err
	}
	if fid.Valid {
		v := fid.Int64
		p.FamilyID = &v
	}
	p.Note = note.String
	p.CreatedAt = parseTime(created)
	return &p, nil
}

func (s *Store) ListPersons(familyID *int64) ([]Person, error) {
	q := `SELECT id, name, family_id, COALESCE(note,''), created_at FROM person`
	args := []any{}
	if familyID != nil {
		q += ` WHERE family_id=?`
		args = append(args, *familyID)
	}
	q += ` ORDER BY id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		var p Person
		var fid sql.NullInt64
		var created string
		if err := rows.Scan(&p.ID, &p.Name, &fid, &p.Note, &created); err != nil {
			return nil, err
		}
		if fid.Valid {
			v := fid.Int64
			p.FamilyID = &v
		}
		p.CreatedAt = parseTime(created)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeletePerson(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM person WHERE id=?`, id)
	return err
}

// ---------- Category ----------

func (s *Store) CreateCategory(c Category) (*Category, error) {
	res, err := s.DB.Exec(`INSERT INTO category(name, kind, parent_id, owner_kind, owner_id, icon)
	                       VALUES(?,?,?,?,?,?)`,
		c.Name, c.Kind, nullableInt(c.ParentID), nullableStr(c.OwnerKind), nullableInt(c.OwnerID), nullIfEmpty(c.Icon))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetCategory(id)
}

func (s *Store) GetCategory(id int64) (*Category, error) {
	var c Category
	var parent, ownerID sql.NullInt64
	var ownerKind, icon sql.NullString
	var created string
	err := s.DB.QueryRow(`SELECT id, name, kind, parent_id, owner_kind, owner_id, icon, created_at
	                      FROM category WHERE id=?`, id).
		Scan(&c.ID, &c.Name, &c.Kind, &parent, &ownerKind, &ownerID, &icon, &created)
	if err != nil {
		return nil, err
	}
	if parent.Valid {
		v := parent.Int64
		c.ParentID = &v
	}
	if ownerKind.Valid {
		v := ownerKind.String
		c.OwnerKind = &v
	}
	if ownerID.Valid {
		v := ownerID.Int64
		c.OwnerID = &v
	}
	c.Icon = icon.String
	c.CreatedAt = parseTime(created)
	return &c, nil
}

// ListCategories lists categories applicable to the given owner scope. Passing
// empty ownerKind returns ALL categories (global + scoped). Otherwise returns
// global (owner_kind IS NULL) plus the ones matching the requested scope.
func (s *Store) ListCategories(kind, ownerKind string, ownerID int64) ([]Category, error) {
	q := `SELECT id, name, kind, parent_id, owner_kind, owner_id, COALESCE(icon,''), created_at FROM category WHERE 1=1`
	args := []any{}
	if kind != "" {
		q += ` AND kind=?`
		args = append(args, kind)
	}
	if ownerKind != "" {
		q += ` AND (owner_kind IS NULL OR (owner_kind=? AND owner_id=?))`
		args = append(args, ownerKind, ownerID)
	}
	q += ` ORDER BY kind, id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		var parent, oid sql.NullInt64
		var ok sql.NullString
		var created string
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &parent, &ok, &oid, &c.Icon, &created); err != nil {
			return nil, err
		}
		if parent.Valid {
			v := parent.Int64
			c.ParentID = &v
		}
		if ok.Valid {
			v := ok.String
			c.OwnerKind = &v
		}
		if oid.Valid {
			v := oid.Int64
			c.OwnerID = &v
		}
		c.CreatedAt = parseTime(created)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteCategory(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM category WHERE id=?`, id)
	return err
}

// FindCategoryByName resolves a category by name (case-insensitive), optionally
// constrained to kind and scope. Returns nil if not found.
func (s *Store) FindCategoryByName(name, kind string) (*Category, error) {
	q := `SELECT id FROM category WHERE lower(name)=lower(?)`
	args := []any{name}
	if kind != "" {
		q += ` AND kind=?`
		args = append(args, kind)
	}
	q += ` ORDER BY owner_id IS NULL DESC LIMIT 1` // prefer global
	var id int64
	if err := s.DB.QueryRow(q, args...).Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return s.GetCategory(id)
}

// ---------- Transaction ----------

func (s *Store) CreateTxn(t Txn) (*Txn, error) {
	if t.Amount <= 0 {
		return nil, fmt.Errorf("amount must be positive (minor units)")
	}
	if t.Currency == "" {
		t.Currency = "CNY"
	}
	if t.Kind != "income" && t.Kind != "expense" {
		return nil, fmt.Errorf("kind must be income or expense")
	}
	if t.OccurredAt.IsZero() {
		t.OccurredAt = time.Now()
	}
	res, err := s.DB.Exec(`INSERT INTO txn(occurred_at, kind, amount, currency,
	                                       category_id, person_id, family_id, payee, note, tags)
	                       VALUES(?,?,?,?,?,?,?,?,?,?)`,
		t.OccurredAt.Format(time.RFC3339),
		t.Kind, t.Amount, t.Currency,
		nullableInt(t.CategoryID),
		nullableInt(t.PersonID),
		nullableInt(t.FamilyID),
		nullIfEmpty(t.Payee), nullIfEmpty(t.Note), nullIfEmpty(t.Tags))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetTxn(id)
}

func (s *Store) GetTxn(id int64) (*Txn, error) {
	var t Txn
	var cat, per, fam sql.NullInt64
	var payee, note, tags sql.NullString
	var occurred, created string
	err := s.DB.QueryRow(`SELECT id, occurred_at, kind, amount, currency,
	                             category_id, person_id, family_id, payee, note, tags, created_at
	                      FROM txn WHERE id=?`, id).
		Scan(&t.ID, &occurred, &t.Kind, &t.Amount, &t.Currency,
			&cat, &per, &fam, &payee, &note, &tags, &created)
	if err != nil {
		return nil, err
	}
	if cat.Valid {
		v := cat.Int64
		t.CategoryID = &v
	}
	if per.Valid {
		v := per.Int64
		t.PersonID = &v
	}
	if fam.Valid {
		v := fam.Int64
		t.FamilyID = &v
	}
	t.Payee = payee.String
	t.Note = note.String
	t.Tags = tags.String
	t.OccurredAt = parseTime(occurred)
	t.CreatedAt = parseTime(created)
	return &t, nil
}

func (s *Store) DeleteTxn(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM txn WHERE id=?`, id)
	return err
}

// UpdateTxnCategory sets txn.category_id. Pass nil to clear it.
// Returns an error if the txn id doesn't exist.
func (s *Store) UpdateTxnCategory(id int64, categoryID *int64) error {
	res, err := s.DB.Exec(`UPDATE txn SET category_id=? WHERE id=?`,
		nullableInt(categoryID), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("txn #%d not found", id)
	}
	return nil
}

// scopeClause returns a SQL fragment + args that restrict `txn` to rows
// belonging to the scope-person's family. Empty when no scope was requested.
//
// A txn is in-scope when ANY of:
//   - txn.person_id is the scope person
//   - txn.person_id belongs to the scope person's family (any sibling)
//   - txn.family_id matches the scope person's family
//
// Unscoped txns (NULL person_id AND NULL family_id) are intentionally invisible
// to scoped readers — they have no owner to authorize against.
func scopeClause(scopePersonID int64) (string, []any) {
	if scopePersonID <= 0 {
		return "", nil
	}
	// Three subqueries against `person` is fine; SQLite caches the plan and the
	// table is tiny (one row per household member).
	frag := ` AND (
		txn.person_id = ?
		OR txn.person_id IN (
			SELECT p.id FROM person p
			WHERE p.family_id IS NOT NULL
			  AND p.family_id = (SELECT family_id FROM person WHERE id = ?)
		)
		OR (txn.family_id IS NOT NULL
		    AND txn.family_id = (SELECT family_id FROM person WHERE id = ?))
	)`
	return frag, []any{scopePersonID, scopePersonID, scopePersonID}
}

// tagClause builds one LIKE predicate per tag and the matching args. Tags are
// stored as a comma-separated list, so we match whole tokens by padding both
// sides with a comma: "新疆" matches ",新疆," but not ",新疆行,". Multiple tags
// are OR'd together. LIKE wildcards in user input are escaped literally.
// Blank tags are ignored; an empty result means "no tag filter".
func tagClause(tags []string) ([]string, []any) {
	var ors []string
	var args []any
	for _, tag := range tags {
		if tag == "" {
			continue
		}
		ors = append(ors, `(',' || COALESCE(tags,'') || ',') LIKE ? ESCAPE '\'`)
		args = append(args, "%,"+escapeLike(tag)+",%")
	}
	return ors, args
}

// escapeLike neutralizes LIKE wildcards so a tag such as "50%" is matched
// literally. The replacer runs in a single pass, so escaped backslashes aren't
// re-escaped. Callers must pair this with `ESCAPE '\'`.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (s *Store) ListTxns(f TxnFilter) ([]Txn, error) {
	q := `SELECT id, occurred_at, kind, amount, currency,
	             category_id, person_id, family_id, COALESCE(payee,''), COALESCE(note,''), COALESCE(tags,''), created_at
	      FROM txn WHERE 1=1`
	args := []any{}
	if f.Since != "" {
		q += ` AND date(occurred_at) >= date(?)`
		args = append(args, f.Since)
	}
	if f.Until != "" {
		q += ` AND date(occurred_at) <= date(?)`
		args = append(args, f.Until)
	}
	if f.Kind != "" {
		q += ` AND kind=?`
		args = append(args, f.Kind)
	}
	if f.PersonID != 0 {
		q += ` AND person_id=?`
		args = append(args, f.PersonID)
	}
	if f.FamilyID != 0 {
		q += ` AND family_id=?`
		args = append(args, f.FamilyID)
	}
	if f.CategoryID != 0 {
		q += ` AND category_id=?`
		args = append(args, f.CategoryID)
	}
	if f.Search != "" {
		q += ` AND (payee LIKE ? OR note LIKE ?)`
		like := "%" + f.Search + "%"
		args = append(args, like, like)
	}
	if ors, tagArgs := tagClause(f.Tags); len(ors) > 0 {
		// OR across tags; AND with every other filter (incl. scope).
		q += ` AND (` + strings.Join(ors, " OR ") + `)`
		args = append(args, tagArgs...)
	}
	if frag, scopeArgs := scopeClause(f.ScopePersonID); frag != "" {
		q += frag
		args = append(args, scopeArgs...)
	}
	q += ` ORDER BY occurred_at DESC, id DESC`
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d OFFSET %d`, f.Limit, f.Offset)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Txn
	for rows.Next() {
		var t Txn
		var cat, per, fam sql.NullInt64
		var occurred, created string
		if err := rows.Scan(&t.ID, &occurred, &t.Kind, &t.Amount, &t.Currency,
			&cat, &per, &fam, &t.Payee, &t.Note, &t.Tags, &created); err != nil {
			return nil, err
		}
		if cat.Valid {
			v := cat.Int64
			t.CategoryID = &v
		}
		if per.Valid {
			v := per.Int64
			t.PersonID = &v
		}
		if fam.Valid {
			v := fam.Int64
			t.FamilyID = &v
		}
		t.OccurredAt = parseTime(occurred)
		t.CreatedAt = parseTime(created)
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---------- Budget ----------

func (s *Store) CreateBudget(b Budget) (*Budget, error) {
	if b.Currency == "" {
		b.Currency = "CNY"
	}
	if err := validateOwner(s, b.OwnerKind, b.OwnerID); err != nil {
		return nil, err
	}
	res, err := s.DB.Exec(`INSERT INTO budget(name, period, amount, currency, category_id, owner_kind, owner_id, start_date, end_date)
	                       VALUES(?,?,?,?,?,?,?,?,?)`,
		b.Name, b.Period, b.Amount, b.Currency, nullableInt(b.CategoryID),
		b.OwnerKind, b.OwnerID, b.StartDate, nullIfEmpty(b.EndDate))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetBudget(id)
}

func (s *Store) GetBudget(id int64) (*Budget, error) {
	var b Budget
	var cat sql.NullInt64
	var end sql.NullString
	var created string
	err := s.DB.QueryRow(`SELECT id, name, period, amount, currency, category_id, owner_kind, owner_id,
	                             start_date, end_date, created_at FROM budget WHERE id=?`, id).
		Scan(&b.ID, &b.Name, &b.Period, &b.Amount, &b.Currency, &cat,
			&b.OwnerKind, &b.OwnerID, &b.StartDate, &end, &created)
	if err != nil {
		return nil, err
	}
	if cat.Valid {
		v := cat.Int64
		b.CategoryID = &v
	}
	b.EndDate = end.String
	b.CreatedAt = parseTime(created)
	return &b, nil
}

// ListBudgets returns budgets visible from the given scope.
//
//   - ownerKind/ownerID, when set, are honored as the legacy "filter to this
//     exact owner" knob.
//   - scopePersonID, when set, restricts results to budgets whose owner is
//     either the scope person, a sibling sharing the same family, or the scope
//     person's family.
func (s *Store) ListBudgets(ownerKind string, ownerID, scopePersonID int64) ([]Budget, error) {
	q := `SELECT id, name, period, amount, currency, category_id, owner_kind, owner_id,
	             start_date, COALESCE(end_date,''), created_at FROM budget WHERE 1=1`
	args := []any{}
	if ownerKind != "" {
		q += ` AND owner_kind=?`
		args = append(args, ownerKind)
	}
	if ownerID != 0 {
		q += ` AND owner_id=?`
		args = append(args, ownerID)
	}
	if scopePersonID > 0 {
		q += ` AND (
			(owner_kind='person' AND owner_id IN (
				SELECT p.id FROM person p
				WHERE p.id = ?
				   OR (p.family_id IS NOT NULL
				       AND p.family_id = (SELECT family_id FROM person WHERE id = ?))
			))
			OR (owner_kind='family' AND owner_id = (
				SELECT family_id FROM person WHERE id = ? AND family_id IS NOT NULL
			))
		)`
		args = append(args, scopePersonID, scopePersonID, scopePersonID)
	}
	q += ` ORDER BY id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Budget
	for rows.Next() {
		var b Budget
		var cat sql.NullInt64
		var created string
		if err := rows.Scan(&b.ID, &b.Name, &b.Period, &b.Amount, &b.Currency, &cat,
			&b.OwnerKind, &b.OwnerID, &b.StartDate, &b.EndDate, &created); err != nil {
			return nil, err
		}
		if cat.Valid {
			v := cat.Int64
			b.CategoryID = &v
		}
		b.CreatedAt = parseTime(created)
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) DeleteBudget(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM budget WHERE id=?`, id)
	return err
}

// BudgetStatus reports spent amount in the current window for a budget.
type BudgetStatus struct {
	Budget    Budget `json:"budget"`
	Spent     int64  `json:"spent"`
	Remaining int64  `json:"remaining"`
	Percent   int    `json:"percent"`
	Since     string `json:"since"`
	Until     string `json:"until"`
}

// BudgetStatusAt computes spent/remaining for a budget at the given reference
// date. The summed txns are those owned by the budget's owner:
//   - owner_kind='person' → txn.person_id matches.
//   - owner_kind='family' → txn.family_id matches OR txn.person_id belongs
//     to that family (treats family-tagged + member-tagged expenses alike).
func (s *Store) BudgetStatusAt(b Budget, ref time.Time) (*BudgetStatus, error) {
	since, until := periodWindow(b.Period, ref)
	q := `SELECT COALESCE(SUM(amount),0) FROM txn
	      WHERE kind='expense'
	        AND date(occurred_at) BETWEEN date(?) AND date(?)`
	args := []any{since, until}
	if b.CategoryID != nil {
		q += ` AND category_id=?`
		args = append(args, *b.CategoryID)
	}
	switch b.OwnerKind {
	case "person":
		q += ` AND person_id=?`
		args = append(args, b.OwnerID)
	case "family":
		q += ` AND (family_id=? OR person_id IN (SELECT id FROM person WHERE family_id=?))`
		args = append(args, b.OwnerID, b.OwnerID)
	default:
		return nil, fmt.Errorf("budget %d has unexpected owner_kind=%q", b.ID, b.OwnerKind)
	}

	var spent int64
	if err := s.DB.QueryRow(q, args...).Scan(&spent); err != nil {
		return nil, err
	}
	pct := 0
	if b.Amount > 0 {
		pct = int(spent * 100 / b.Amount)
	}
	return &BudgetStatus{
		Budget: b, Spent: spent, Remaining: b.Amount - spent,
		Percent: pct, Since: since, Until: until,
	}, nil
}

func periodWindow(period string, ref time.Time) (string, string) {
	y, m, _ := ref.Date()
	switch period {
	case "weekly":
		// ISO week: Monday..Sunday
		weekday := int(ref.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		start := ref.AddDate(0, 0, -(weekday - 1))
		end := start.AddDate(0, 0, 6)
		return start.Format("2006-01-02"), end.Format("2006-01-02")
	case "yearly":
		return fmt.Sprintf("%04d-01-01", y), fmt.Sprintf("%04d-12-31", y)
	default: // monthly
		start := time.Date(y, m, 1, 0, 0, 0, 0, ref.Location())
		end := start.AddDate(0, 1, -1)
		return start.Format("2006-01-02"), end.Format("2006-01-02")
	}
}

// ---------- Reports ----------

func (s *Store) Summarize(f TxnFilter) (*Summary, error) {
	txns, err := s.ListTxns(f)
	if err != nil {
		return nil, err
	}
	sum := &Summary{Since: f.Since, Until: f.Until, TxnCount: len(txns)}
	byCat := map[int64]*CategoryAmount{}
	noCat := &CategoryAmount{CategoryName: "(未分类)", Kind: "expense"}
	for _, t := range txns {
		if sum.Currency == "" {
			sum.Currency = t.Currency
		}
		switch t.Kind {
		case "income":
			sum.Income += t.Amount
			addCat(byCat, noCat, t, s)
		case "expense":
			sum.Expense += t.Amount
			addCat(byCat, noCat, t, s)
		}
	}
	sum.Net = sum.Income - sum.Expense
	for _, c := range byCat {
		sum.ByCategory = append(sum.ByCategory, *c)
	}
	if noCat.Amount > 0 {
		sum.ByCategory = append(sum.ByCategory, *noCat)
	}
	return sum, nil
}

func addCat(byCat map[int64]*CategoryAmount, noCat *CategoryAmount, t Txn, s *Store) {
	if t.CategoryID == nil {
		noCat.Amount += t.Amount
		noCat.Kind = t.Kind
		return
	}
	cid := *t.CategoryID
	ca, ok := byCat[cid]
	if !ok {
		c, _ := s.GetCategory(cid)
		name := fmt.Sprintf("category#%d", cid)
		if c != nil {
			name = c.Name
		}
		ca = &CategoryAmount{CategoryID: &cid, CategoryName: name, Kind: t.Kind}
		byCat[cid] = ca
	}
	ca.Amount += t.Amount
}

// ---------- Helpers ----------

func validateOwner(s *Store, kind string, id int64) error {
	switch kind {
	case "family":
		_, err := s.GetFamily(id)
		if err != nil {
			return fmt.Errorf("family %d not found: %w", id, err)
		}
	case "person":
		_, err := s.GetPerson(id)
		if err != nil {
			return fmt.Errorf("person %d not found: %w", id, err)
		}
	default:
		return fmt.Errorf("owner_kind must be 'person' or 'family'")
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullableStr(p *string) any {
	if p == nil || *p == "" {
		return nil
	}
	return *p
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05Z", "2006-01-02"}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ParseAmount converts a human decimal string like "12.34" or "12" to minor units (1234 / 1200).
// Supports leading +/-, up to 2 fractional digits.
func ParseAmount(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	parts := strings.SplitN(s, ".", 2)
	var whole, frac int64
	var err error
	if _, err = fmt.Sscanf(parts[0], "%d", &whole); err != nil {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	if len(parts) == 2 {
		p := parts[1]
		switch len(p) {
		case 0:
			frac = 0
		case 1:
			if _, err = fmt.Sscanf(p, "%d", &frac); err != nil {
				return 0, fmt.Errorf("invalid amount %q", s)
			}
			frac *= 10
		case 2:
			if _, err = fmt.Sscanf(p, "%d", &frac); err != nil {
				return 0, fmt.Errorf("invalid amount %q", s)
			}
		default:
			return 0, fmt.Errorf("amount has too many decimals: %q", s)
		}
	}
	v := whole*100 + frac
	if neg {
		v = -v
	}
	return v, nil
}

// FormatAmount renders minor units as "123.45".
func FormatAmount(v int64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	whole := v / 100
	frac := v % 100
	s := fmt.Sprintf("%d.%02d", whole, frac)
	if neg {
		return "-" + s
	}
	return s
}
