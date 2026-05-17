package main

import (
	"encoding/json"
	"fmt"
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
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

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

		// ---- account ----
		{
			Name: "create_account",
			Description: "Create an account. owner_kind is 'person' or 'family'. " +
				"initial_balance is a decimal string/number (e.g. \"100.00\").",
			InputSchema: obj(map[string]any{
				"name":            strProp("Account name"),
				"type":            strProp("cash|bank|credit|investment|virtual"),
				"currency":        strProp("ISO code, default CNY"),
				"initial_balance": numProp("Decimal amount"),
				"owner_kind":      strProp("person|family"),
				"owner_id":        intProp("Owner id"),
				"note":            strProp("Optional note"),
			}, "name", "type", "owner_kind", "owner_id"),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					Name           string `json:"name"`
					Type           string `json:"type"`
					Currency       string `json:"currency"`
					InitialBalance any    `json:"initial_balance"`
					OwnerKind      string `json:"owner_kind"`
					OwnerID        int64  `json:"owner_id"`
					Note           string `json:"note"`
				}
				if err := parseArgs(a, &p); err != nil {
					return nil, err
				}
				var init int64
				if p.InitialBalance != nil {
					v, err := moneyArg(p.InitialBalance)
					if err != nil {
						return nil, err
					}
					init = v
				}
				return s.CreateAccount(store.Account{
					Name: p.Name, Type: p.Type, Currency: p.Currency,
					InitialBalance: init, OwnerKind: p.OwnerKind, OwnerID: p.OwnerID,
					Note: p.Note,
				})
			},
		},
		{
			Name:        "list_accounts",
			Description: "List accounts, optionally scoped to an owner. include_archived to show hidden.",
			InputSchema: obj(map[string]any{
				"owner_kind":       strProp("person|family"),
				"owner_id":         intProp("Owner id"),
				"include_archived": boolProp("Include archived accounts"),
			}),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					OwnerKind       string `json:"owner_kind"`
					OwnerID         int64  `json:"owner_id"`
					IncludeArchived bool   `json:"include_archived"`
				}
				parseArgs(a, &p)
				return s.ListAccounts(p.OwnerKind, p.OwnerID, p.IncludeArchived)
			},
		},
		{
			Name:        "archive_account",
			Description: "Archive (or unarchive) an account.",
			InputSchema: obj(map[string]any{
				"id":       intProp("account id"),
				"archived": boolProp("true=archive, false=unarchive (default true)"),
			}, "id"),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					ID       int64
					Archived *bool
				}
				parseArgs(a, &p)
				arch := true
				if p.Archived != nil {
					arch = *p.Archived
				}
				return map[string]any{"ok": true}, s.ArchiveAccount(p.ID, arch)
			},
		},
		{
			Name:        "account_balances",
			Description: "Current balance for each account (initial + income + incoming transfers − expense − outgoing transfers). Optional owner filter.",
			InputSchema: obj(map[string]any{
				"owner_kind": strProp("person|family"),
				"owner_id":   intProp("Owner id"),
			}),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					OwnerKind string `json:"owner_kind"`
					OwnerID   int64  `json:"owner_id"`
				}
				parseArgs(a, &p)
				return s.AccountBalances(p.OwnerKind, p.OwnerID)
			},
		},

		// ---- category ----
		{
			Name:        "create_category",
			Description: "Create a category. Omit owner_kind/owner_id for a global category.",
			InputSchema: obj(map[string]any{
				"name":       strProp("Category name"),
				"kind":       strProp("income|expense|transfer"),
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
				"kind":       strProp("income|expense|transfer"),
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
				"date defaults to today; amount is decimal.",
			InputSchema: obj(map[string]any{
				"amount":        numProp("Decimal amount"),
				"account_id":    intProp("Source account id"),
				"category_id":   intProp("Category id"),
				"category_name": strProp("Category name (resolved to id if provided)"),
				"person_id":     intProp("Who made the expense"),
				"family_id":     intProp("Which family ledger"),
				"date":          strProp("YYYY-MM-DD"),
				"payee":         strProp("Payee/merchant"),
				"note":          strProp("Note"),
				"tags":          strProp("Comma-separated tags"),
				"currency":      strProp("Currency, default CNY"),
			}, "amount", "account_id"),
			handler: func(a json.RawMessage) (any, error) {
				return addTxn(s, a, "expense")
			},
		},
		{
			Name:        "add_income",
			Description: "Record an income. Args mirror add_expense.",
			InputSchema: obj(map[string]any{
				"amount":        numProp("Decimal amount"),
				"account_id":    intProp("Destination account id"),
				"category_id":   intProp("Category id"),
				"category_name": strProp("Category name"),
				"person_id":     intProp("Who received"),
				"family_id":     intProp("Family ledger"),
				"date":          strProp("YYYY-MM-DD"),
				"payee":         strProp("Payer"),
				"note":          strProp("Note"),
				"tags":          strProp("Tags"),
				"currency":      strProp("Currency"),
			}, "amount", "account_id"),
			handler: func(a json.RawMessage) (any, error) {
				return addTxn(s, a, "income")
			},
		},
		{
			Name:        "add_transfer",
			Description: "Record a transfer between two accounts. amount > 0 moves from `from_account_id` to `to_account_id`.",
			InputSchema: obj(map[string]any{
				"amount":          numProp("Decimal amount"),
				"from_account_id": intProp("Source account"),
				"to_account_id":   intProp("Destination account"),
				"date":            strProp("YYYY-MM-DD"),
				"note":            strProp("Note"),
				"currency":        strProp("Currency"),
			}, "amount", "from_account_id", "to_account_id"),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					Amount        any
					FromAccountID int64  `json:"from_account_id"`
					ToAccountID   int64  `json:"to_account_id"`
					Date          string `json:"date"`
					Note          string `json:"note"`
					Currency      string `json:"currency"`
				}
				if err := parseArgs(a, &p); err != nil {
					return nil, err
				}
				amt, err := moneyArg(p.Amount)
				if err != nil {
					return nil, err
				}
				when, err := parseDate(p.Date)
				if err != nil {
					return nil, err
				}
				to := p.ToAccountID
				return s.CreateTxn(store.Txn{
					OccurredAt: when, Kind: "transfer", Amount: amt,
					Currency: p.Currency, AccountID: p.FromAccountID,
					CounterAccountID: &to, Note: p.Note,
				})
			},
		},
		{
			Name:        "list_transactions",
			Description: "Query transactions with filters (date range, kind, account, person, family, category, search).",
			InputSchema: obj(map[string]any{
				"since":       strProp("Start date YYYY-MM-DD"),
				"until":       strProp("End date YYYY-MM-DD"),
				"kind":        strProp("income|expense|transfer"),
				"account_id":  intProp("Account id"),
				"person_id":   intProp("Person id"),
				"family_id":   intProp("Family id"),
				"category_id": intProp("Category id"),
				"search":      strProp("Substring match in payee/note"),
				"limit":       intProp("Max rows, default 50"),
				"offset":      intProp("Offset, default 0"),
			}),
			handler: func(a json.RawMessage) (any, error) {
				var f store.TxnFilter
				parseArgs(a, &f)
				if f.Limit == 0 {
					f.Limit = 50
				}
				return s.ListTxns(f)
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
			Description: "List budgets; optionally filter by owner.",
			InputSchema: obj(map[string]any{
				"owner_kind": strProp("person|family"),
				"owner_id":   intProp("Owner id"),
			}),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					OwnerKind string `json:"owner_kind"`
					OwnerID   int64  `json:"owner_id"`
				}
				parseArgs(a, &p)
				return s.ListBudgets(p.OwnerKind, p.OwnerID)
			},
		},
		{
			Name:        "budget_status",
			Description: "Compute spent/remaining for each budget at a reference date (default today).",
			InputSchema: obj(map[string]any{
				"owner_kind": strProp("person|family"),
				"owner_id":   intProp("Owner id"),
				"date":       strProp("YYYY-MM-DD"),
			}),
			handler: func(a json.RawMessage) (any, error) {
				var p struct {
					OwnerKind string `json:"owner_kind"`
					OwnerID   int64  `json:"owner_id"`
					Date      string `json:"date"`
				}
				parseArgs(a, &p)
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
				bs, err := s.ListBudgets(p.OwnerKind, p.OwnerID)
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
			Name:        "summarize",
			Description: "Summarize income/expense for a period with breakdowns by category and account. Defaults to current month when since/until omitted.",
			InputSchema: obj(map[string]any{
				"since":      strProp("YYYY-MM-DD"),
				"until":      strProp("YYYY-MM-DD"),
				"person_id":  intProp("Filter by person"),
				"family_id":  intProp("Filter by family"),
				"account_id": intProp("Filter by account"),
			}),
			handler: func(a json.RawMessage) (any, error) {
				var f store.TxnFilter
				parseArgs(a, &f)
				if f.Since == "" && f.Until == "" {
					now := time.Now()
					start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
					end := start.AddDate(0, 1, -1)
					f.Since = start.Format("2006-01-02")
					f.Until = end.Format("2006-01-02")
				}
				return s.Summarize(f)
			},
		},
	}
}

// addTxn is the shared handler for add_expense / add_income.
func addTxn(s *store.Store, a json.RawMessage, kind string) (any, error) {
	var p struct {
		Amount       any
		AccountID    int64  `json:"account_id"`
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
		AccountID: p.AccountID, CategoryID: catID,
		PersonID: p.PersonID, FamilyID: p.FamilyID,
		Payee: p.Payee, Note: p.Note, Tags: p.Tags,
	})
}
