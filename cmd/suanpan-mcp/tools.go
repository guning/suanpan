package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/goose/suanpan/internal/store"
)

// ---- schema helpers ----

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func strProp(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func intProp(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
func numProp(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }

// parseArgs unmarshals args into out. A nil / empty payload is treated as "{}".
func parseArgs(args json.RawMessage, out any) error {
	if len(args) == 0 || string(args) == "null" {
		return nil
	}
	return json.Unmarshal(args, out)
}

// moneyArg accepts either a string decimal ("12.34") or a JSON number and
// returns the amount in minor units.
func moneyArg(v any) (int64, error) {
	switch x := v.(type) {
	case string:
		return store.ParseAmount(x)
	case float64:
		return store.ParseAmount(fmt.Sprintf("%.2f", x))
	case int:
		return int64(x) * 100, nil
	case nil:
		return 0, fmt.Errorf("missing amount")
	default:
		return 0, fmt.Errorf("amount must be string or number, got %T", v)
	}
}

func ptrInt64(v int64) *int64 { return &v }

func parseDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	for _, l := range []string{"2006-01-02", "2006-01-02 15:04", "2006-01-02T15:04:05", time.RFC3339} {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse date %q (want YYYY-MM-DD)", s)
}

// resolveScopePerson picks the caller identity for scoped reads, preferring
// the explicit as_person_id argument and falling back to $SUANPAN_AS_PERSON.
func resolveScopePerson(arg int64) (int64, error) {
	if arg > 0 {
		return arg, nil
	}
	if env := os.Getenv("SUANPAN_AS_PERSON"); env != "" {
		v, err := strconv.ParseInt(env, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("$SUANPAN_AS_PERSON=%q: %w", env, err)
		}
		if v <= 0 {
			return 0, fmt.Errorf("$SUANPAN_AS_PERSON must be a positive person id")
		}
		return v, nil
	}
	return 0, errors.New("as_person_id is required (or set $SUANPAN_AS_PERSON); scoped reads only return your family's data")
}

// ---- tool registry ----

func buildTools(s *store.Store) []toolDef {
	return []toolDef{
		{
			Name:        "init_db",
			Description: "Apply schema and seed default categories. Safe to call repeatedly.",
			InputSchema: obj(map[string]any{}),
			handler: func(_ json.RawMessage) (any, error) {
				if err := s.Init(); err != nil {
					return nil, err
				}
				return map[string]any{"path": s.Path, "ok": true}, nil
			},
		},

		// ---- family ----
		{
			Name:        "create_family",
			Description: "Create a family (household). Returns the created record.",
			InputSchema: obj(map[string]any{
				"name":     strProp("Family name, e.g. 张家"),
				"currency": strProp("ISO code, default CNY"),
				"note":     strProp("Optional note"),
			}, "name"),
			handler: func(a json.RawMessage) (any, error) {
				var p struct{ Name, Currency, Note string }
				if err := parseArgs(a, &p); err != nil {
					return nil, err
				}
				return s.CreateFamily(p.Name, p.Currency, p.Note)
			},
		},
		{
			Name:        "list_families",
			Description: "List all families.",
			InputSchema: obj(map[string]any{}),
			handler: func(_ json.RawMessage) (any, error) {
				return s.ListFamilies()
			},
		},
		{
			Name:        "delete_family",
			Description: "Delete a family by id.",
			InputSchema: obj(map[string]any{"id": intProp("family id")}, "id"),
			handler: func(a json.RawMessage) (any, error) {
				var p struct{ ID int64 }
				if err := parseArgs(a, &p); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, s.DeleteFamily(p.ID)
			},
		},

		// ---- person ----
		{
			Name:        "create_person",
			Description: "Create a person. Optionally attach to a family.",
			InputSchema: obj(map[string]any{
				"name":      strProp("Person name"),
				"family_id": intProp("Optional family id"),
				"note":      strProp("Optional note"),
			}, "name"),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					Name     string
					FamilyID *int64 `json:"family_id"`
					Note     string
				}
				if err := parseArgs(a, &p); err != nil {
					return nil, err
				}
				return s.CreatePerson(p.Name, p.FamilyID, p.Note)
			},
		},
		{
			Name:        "list_persons",
			Description: "List persons, optionally scoped to a family.",
			InputSchema: obj(map[string]any{"family_id": intProp("Optional filter")}),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					FamilyID *int64 `json:"family_id"`
				}
				parseArgs(a, &p)
				return s.ListPersons(p.FamilyID)
			},
		},
		{
			Name:        "delete_person",
			Description: "Delete a person by id.",
			InputSchema: obj(map[string]any{"id": intProp("person id")}, "id"),
			handler: func(a json.RawMessage) (any, error) {
				var p struct{ ID int64 }
				parseArgs(a, &p)
				return map[string]any{"ok": true}, s.DeletePerson(p.ID)
			},
		},

		// ---- category ----
		{
			Name:        "create_category",
			Description: "Create a category. Omit owner_kind/owner_id for a global category.",
			InputSchema: obj(map[string]any{
				"name":       strProp("Category name"),
				"kind":       strProp("income|expense"),
				"parent_id":  intProp("Parent category id"),
				"owner_kind": strProp("person|family (or empty for global)"),
				"owner_id":   intProp("Owner id"),
				"icon":       strProp("Emoji/icon"),
			}, "name", "kind"),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					Name, Kind string
					ParentID   *int64  `json:"parent_id"`
					OwnerKind  *string `json:"owner_kind"`
					OwnerID    *int64  `json:"owner_id"`
					Icon       string
				}
				if err := parseArgs(a, &p); err != nil {
					return nil, err
				}
				return s.CreateCategory(store.Category{
					Name: p.Name, Kind: p.Kind, ParentID: p.ParentID,
					OwnerKind: p.OwnerKind, OwnerID: p.OwnerID, Icon: p.Icon,
				})
			},
		},
		{
			Name:        "list_categories",
			Description: "List categories. Empty owner_kind returns all; otherwise returns global + scoped.",
			InputSchema: obj(map[string]any{
				"kind":       strProp("income|expense"),
				"owner_kind": strProp("person|family"),
				"owner_id":   intProp("Owner id"),
			}),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					Kind      string
					OwnerKind string `json:"owner_kind"`
					OwnerID   int64  `json:"owner_id"`
				}
				parseArgs(a, &p)
				return s.ListCategories(p.Kind, p.OwnerKind, p.OwnerID)
			},
		},
		{
			Name:        "delete_category",
			Description: "Delete a category by id.",
			InputSchema: obj(map[string]any{"id": intProp("category id")}, "id"),
			handler: func(a json.RawMessage) (any, error) {
				var p struct{ ID int64 }
				parseArgs(a, &p)
				return map[string]any{"ok": true}, s.DeleteCategory(p.ID)
			},
		},

		// ---- transactions ----
		{
			Name: "add_expense",
			Description: "Record an expense. Either category_id or category_name may be supplied. " +
				"At least one of person_id / family_id is required (every txn has an owner). " +
				"date defaults to today; amount is decimal.",
			InputSchema: obj(map[string]any{
				"amount":        numProp("Decimal amount"),
				"category_id":   intProp("Category id"),
				"category_name": strProp("Category name (resolved to id if provided)"),
				"person_id":     intProp("Who made the expense"),
				"family_id":     intProp("Which family ledger"),
				"date":          strProp("YYYY-MM-DD"),
				"payee":         strProp("Payee/merchant"),
				"note":          strProp("Note"),
				"tags":          strProp("Comma-separated tags"),
				"currency":      strProp("Currency, default CNY"),
			}, "amount"),
			handler: func(a json.RawMessage) (any, error) {
				return addTxn(s, a, "expense")
			},
		},
		{
			Name:        "add_income",
			Description: "Record an income. Args mirror add_expense.",
			InputSchema: obj(map[string]any{
				"amount":        numProp("Decimal amount"),
				"category_id":   intProp("Category id"),
				"category_name": strProp("Category name"),
				"person_id":     intProp("Who received"),
				"family_id":     intProp("Family ledger"),
				"date":          strProp("YYYY-MM-DD"),
				"payee":         strProp("Payer"),
				"note":          strProp("Note"),
				"tags":          strProp("Tags"),
				"currency":      strProp("Currency"),
			}, "amount"),
			handler: func(a json.RawMessage) (any, error) {
				return addTxn(s, a, "income")
			},
		},
		{
			Name: "list_transactions",
			Description: "Query transactions with filters. as_person_id (or $SUANPAN_AS_PERSON) " +
				"scopes results to the caller's family — other families are invisible.",
			InputSchema: obj(map[string]any{
				"as_person_id": intProp("Caller identity for family scope (or $SUANPAN_AS_PERSON)"),
				"since":        strProp("Start date YYYY-MM-DD"),
				"until":        strProp("End date YYYY-MM-DD"),
				"kind":         strProp("income|expense"),
				"person_id":    intProp("Person id"),
				"family_id":    intProp("Family id"),
				"category_id":  intProp("Category id"),
				"search":       strProp("Substring match in payee/note"),
				"limit":        intProp("Max rows, default 50"),
				"offset":       intProp("Offset, default 0"),
			}),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					AsPersonID int64 `json:"as_person_id"`
					store.TxnFilter
				}
				parseArgs(a, &p)
				scope, err := resolveScopePerson(p.AsPersonID)
				if err != nil {
					return nil, err
				}
				p.TxnFilter.ScopePersonID = scope
				if p.TxnFilter.Limit == 0 {
					p.TxnFilter.Limit = 50
				}
				return s.ListTxns(p.TxnFilter)
			},
		},
		{
			Name:        "delete_transaction",
			Description: "Delete a transaction by id.",
			InputSchema: obj(map[string]any{"id": intProp("txn id")}, "id"),
			handler: func(a json.RawMessage) (any, error) {
				var p struct{ ID int64 }
				parseArgs(a, &p)
				return map[string]any{"ok": true}, s.DeleteTxn(p.ID)
			},
		},

		// ---- budget ----
		{
			Name:        "create_budget",
			Description: "Create a budget (weekly/monthly/yearly) for a person or family, optionally scoped to a category.",
			InputSchema: obj(map[string]any{
				"name":        strProp("Budget name"),
				"period":      strProp("weekly|monthly|yearly"),
				"amount":      numProp("Decimal amount"),
				"currency":    strProp("Currency"),
				"category_id": intProp("Category id (optional; blank = overall)"),
				"owner_kind":  strProp("person|family"),
				"owner_id":    intProp("Owner id"),
				"start_date":  strProp("YYYY-MM-DD"),
				"end_date":    strProp("YYYY-MM-DD (optional)"),
			}, "name", "period", "amount", "owner_kind", "owner_id", "start_date"),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					Name, Period string
					Amount       any
					Currency     string
					CategoryID   *int64 `json:"category_id"`
					OwnerKind    string `json:"owner_kind"`
					OwnerID      int64  `json:"owner_id"`
					StartDate    string `json:"start_date"`
					EndDate      string `json:"end_date"`
				}
				if err := parseArgs(a, &p); err != nil {
					return nil, err
				}
				amt, err := moneyArg(p.Amount)
				if err != nil {
					return nil, err
				}
				return s.CreateBudget(store.Budget{
					Name: p.Name, Period: p.Period, Amount: amt, Currency: p.Currency,
					CategoryID: p.CategoryID, OwnerKind: p.OwnerKind, OwnerID: p.OwnerID,
					StartDate: p.StartDate, EndDate: p.EndDate,
				})
			},
		},
		{
			Name:        "list_budgets",
			Description: "List budgets visible to the caller; family-scoped via as_person_id.",
			InputSchema: obj(map[string]any{
				"as_person_id": intProp("Caller identity (or $SUANPAN_AS_PERSON)"),
				"owner_kind":   strProp("person|family — optional narrower filter"),
				"owner_id":     intProp("Owner id"),
			}),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					AsPersonID int64  `json:"as_person_id"`
					OwnerKind  string `json:"owner_kind"`
					OwnerID    int64  `json:"owner_id"`
				}
				parseArgs(a, &p)
				scope, err := resolveScopePerson(p.AsPersonID)
				if err != nil {
					return nil, err
				}
				return s.ListBudgets(p.OwnerKind, p.OwnerID, scope)
			},
		},
		{
			Name:        "budget_status",
			Description: "Compute spent/remaining for each visible budget at a reference date (default today).",
			InputSchema: obj(map[string]any{
				"as_person_id": intProp("Caller identity (or $SUANPAN_AS_PERSON)"),
				"owner_kind":   strProp("person|family — optional narrower filter"),
				"owner_id":     intProp("Owner id"),
				"date":         strProp("YYYY-MM-DD"),
			}),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					AsPersonID int64  `json:"as_person_id"`
					OwnerKind  string `json:"owner_kind"`
					OwnerID    int64  `json:"owner_id"`
					Date       string `json:"date"`
				}
				parseArgs(a, &p)
				scope, err := resolveScopePerson(p.AsPersonID)
				if err != nil {
					return nil, err
				}
				ref := time.Now()
				if p.Date != "" {
					t, err := parseDate(p.Date)
					if err != nil {
						return nil, err
					}
					if !t.IsZero() {
						ref = t
					}
				}
				bs, err := s.ListBudgets(p.OwnerKind, p.OwnerID, scope)
				if err != nil {
					return nil, err
				}
				var out []*store.BudgetStatus
				for _, b := range bs {
					st, err := s.BudgetStatusAt(b, ref)
					if err != nil {
						return nil, err
					}
					out = append(out, st)
				}
				return out, nil
			},
		},
		{
			Name:        "delete_budget",
			Description: "Delete a budget by id.",
			InputSchema: obj(map[string]any{"id": intProp("budget id")}, "id"),
			handler: func(a json.RawMessage) (any, error) {
				var p struct{ ID int64 }
				parseArgs(a, &p)
				return map[string]any{"ok": true}, s.DeleteBudget(p.ID)
			},
		},

		// ---- report ----
		{
			Name: "summarize",
			Description: "Summarize income/expense for a period with breakdown by category. " +
				"Defaults to current month when since/until omitted. " +
				"as_person_id (or $SUANPAN_AS_PERSON) scopes results to the caller's family.",
			InputSchema: obj(map[string]any{
				"as_person_id": intProp("Caller identity (or $SUANPAN_AS_PERSON)"),
				"since":        strProp("YYYY-MM-DD"),
				"until":        strProp("YYYY-MM-DD"),
				"person_id":    intProp("Filter by person"),
				"family_id":    intProp("Filter by family"),
			}),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					AsPersonID int64 `json:"as_person_id"`
					store.TxnFilter
				}
				parseArgs(a, &p)
				scope, err := resolveScopePerson(p.AsPersonID)
				if err != nil {
					return nil, err
				}
				p.TxnFilter.ScopePersonID = scope
				if p.TxnFilter.Since == "" && p.TxnFilter.Until == "" {
					now := time.Now()
					start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
					end := start.AddDate(0, 1, -1)
					p.TxnFilter.Since = start.Format("2006-01-02")
					p.TxnFilter.Until = end.Format("2006-01-02")
				}
				return s.Summarize(p.TxnFilter)
			},
		},
	}
}

// addTxn is the shared handler for add_expense / add_income.
func addTxn(s *store.Store, a json.RawMessage, kind string) (any, error) {
	var p struct {
		Amount       any
		CategoryID   *int64 `json:"category_id"`
		CategoryName string `json:"category_name"`
		PersonID     *int64 `json:"person_id"`
		FamilyID     *int64 `json:"family_id"`
		Date         string
		Payee, Note  string
		Tags         string
		Currency     string
	}
	if err := parseArgs(a, &p); err != nil {
		return nil, err
	}
	if p.PersonID == nil && p.FamilyID == nil {
		return nil, errors.New("person_id or family_id is required (every txn must have an owner)")
	}
	amt, err := moneyArg(p.Amount)
	if err != nil {
		return nil, err
	}
	when, err := parseDate(p.Date)
	if err != nil {
		return nil, err
	}
	catID := p.CategoryID
	if catID == nil && p.CategoryName != "" {
		c, err := s.FindCategoryByName(p.CategoryName, kind)
		if err != nil {
			return nil, err
		}
		if c == nil {
			return nil, fmt.Errorf("no category matches %q for kind=%s", p.CategoryName, kind)
		}
		catID = ptrInt64(c.ID)
	}
	return s.CreateTxn(store.Txn{
		OccurredAt: when, Kind: kind, Amount: amt, Currency: p.Currency,
		CategoryID: catID,
		PersonID:   p.PersonID, FamilyID: p.FamilyID,
		Payee: p.Payee, Note: p.Note, Tags: p.Tags,
	})
}
