// suanpan — local-first accounting CLI backed by SQLite.
//
// Run `suanpan help` for a quick subcommand tour. The DB lives at
// $SUANPAN_DB (default ~/.suanpan/suanpan.db). Money is accepted and rendered as
// decimals (e.g. "12.34"), stored internally as integer minor units.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/goose/suanpan/internal/store"
)

var dbPath string

func main() {
	rootFS := flag.NewFlagSet("suanpan", flag.ContinueOnError)
	rootFS.StringVar(&dbPath, "db", "", "SQLite DB path (default $SUANPAN_DB or ~/.suanpan/suanpan.db)")
	rootFS.Usage = usage
	// Parse only global flags before first positional.
	args := os.Args[1:]
	// Allow `-db ... subcommand ...` or `subcommand ...`
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if err := rootFS.Parse(args); err != nil {
			os.Exit(2)
		}
		args = rootFS.Args()
		break
	}
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	cmd, rest := args[0], args[1:]

	dispatch := map[string]func([]string) error{
		"help":          func(_ []string) error { usage(); return nil },
		"init":          cmdInit,
		"family":        cmdFamily,
		"person":        cmdPerson,
		"category":      cmdCategory,
		"txn":           cmdTxn,
		"budget":        cmdBudget,
		"report":        cmdReport,
		"install-skill": cmdInstallSkill,
	}

	fn, ok := dispatch[cmd]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err := fn(rest); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `suanpan — local-first accounting

USAGE:
  suanpan [-db PATH] <command> [flags]

COMMANDS:
  init                     Create schema and seed default categories
  family add|list|rm       Manage families
  person add|list|rm       Manage persons
  category add|list|rm     Manage categories
  txn add|list|rm          Manage transactions
  budget add|list|status|rm
  report                   Summarize income/expense for a period
  install-skill            Install the Claude Code skill + print MCP setup

GLOBAL FLAGS:
  -db PATH   SQLite DB path (env $SUANPAN_DB, default ~/.suanpan/suanpan.db)

Read commands (report, txn list, budget list, budget status) require a
caller identity for family-scoping: pass -as-person <id>, or export
$SUANPAN_AS_PERSON. Results are restricted to that person, their family
siblings, and the family ledger; other families' data is hidden.

Examples:
  suanpan init
  suanpan family add -name "张家" -currency CNY
  suanpan person add -name "张三" -family 1
  suanpan txn add -amount 23.5 -kind expense -category-name 餐饮 -person 1
  suanpan txn list -as-person 1 -since 2026-04-01
  suanpan report -as-person 1 -since 2026-04-01 -until 2026-04-30
`)
}

// ---- helpers ----

func openStore() (*store.Store, error) {
	return store.Open(dbPath)
}

// parseOwner parses "kind:id" into (kind, id).
func parseOwner(s string) (string, int64, error) {
	if s == "" {
		return "", 0, nil
	}
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("owner must be kind:id (e.g. person:1 or family:2)")
	}
	kind := parts[0]
	if kind != "person" && kind != "family" {
		return "", 0, fmt.Errorf("owner kind must be person or family")
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("owner id: %w", err)
	}
	return kind, id, nil
}

// resolveScopePerson picks the caller identity for scoped reads, preferring
// the explicit -as-person flag and falling back to $SUANPAN_AS_PERSON.
// Returns an error if neither is set — scoped reads MUST identify a caller.
func resolveScopePerson(flagVal int64) (int64, error) {
	if flagVal > 0 {
		return flagVal, nil
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
	return 0, errors.New("-as-person <id> is required (or set $SUANPAN_AS_PERSON); scoped reads only return your family's data")
}

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func tw() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
}

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

// ---- init ----

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	fs.Parse(args)
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.Init(); err != nil {
		return err
	}
	fmt.Printf("initialized DB at %s\n", s.Path)
	return nil
}

// ---- family ----

func cmdFamily(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: suanpan family <add|list|rm> ...")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		fs := flag.NewFlagSet("family add", flag.ExitOnError)
		name := fs.String("name", "", "family name (required)")
		currency := fs.String("currency", "CNY", "currency")
		note := fs.String("note", "", "note")
		asJSON := fs.Bool("json", false, "output JSON")
		fs.Parse(rest)
		if *name == "" {
			return errors.New("-name is required")
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		f, err := s.CreateFamily(*name, *currency, *note)
		if err != nil {
			return err
		}
		if *asJSON {
			return emitJSON(f)
		}
		fmt.Printf("family #%d %s (%s)\n", f.ID, f.Name, f.Currency)
	case "list":
		fs := flag.NewFlagSet("family list", flag.ExitOnError)
		asJSON := fs.Bool("json", false, "output JSON")
		fs.Parse(rest)
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		fams, err := s.ListFamilies()
		if err != nil {
			return err
		}
		if *asJSON {
			return emitJSON(fams)
		}
		w := tw()
		fmt.Fprintln(w, "ID\tNAME\tCURRENCY\tNOTE\tCREATED")
		for _, f := range fams {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", f.ID, f.Name, f.Currency, f.Note, f.CreatedAt.Format("2006-01-02"))
		}
		w.Flush()
	case "rm":
		fs := flag.NewFlagSet("family rm", flag.ExitOnError)
		id := fs.Int64("id", 0, "family id")
		fs.Parse(rest)
		if *id == 0 {
			return errors.New("-id is required")
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		return s.DeleteFamily(*id)
	default:
		return fmt.Errorf("unknown family subcommand: %s", sub)
	}
	return nil
}

// ---- person ----

func cmdPerson(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: suanpan person <add|list|rm> ...")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		fs := flag.NewFlagSet("person add", flag.ExitOnError)
		name := fs.String("name", "", "person name (required)")
		family := fs.Int64("family", 0, "family id (optional)")
		note := fs.String("note", "", "note")
		asJSON := fs.Bool("json", false, "output JSON")
		fs.Parse(rest)
		if *name == "" {
			return errors.New("-name is required")
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		var fid *int64
		if *family != 0 {
			fid = family
		}
		p, err := s.CreatePerson(*name, fid, *note)
		if err != nil {
			return err
		}
		if *asJSON {
			return emitJSON(p)
		}
		fmt.Printf("person #%d %s", p.ID, p.Name)
		if p.FamilyID != nil {
			fmt.Printf(" (family %d)", *p.FamilyID)
		}
		fmt.Println()
	case "list":
		fs := flag.NewFlagSet("person list", flag.ExitOnError)
		family := fs.Int64("family", 0, "filter by family id")
		asJSON := fs.Bool("json", false, "output JSON")
		fs.Parse(rest)
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		var fid *int64
		if *family != 0 {
			fid = family
		}
		ps, err := s.ListPersons(fid)
		if err != nil {
			return err
		}
		if *asJSON {
			return emitJSON(ps)
		}
		w := tw()
		fmt.Fprintln(w, "ID\tNAME\tFAMILY\tNOTE\tCREATED")
		for _, p := range ps {
			fam := "-"
			if p.FamilyID != nil {
				fam = fmt.Sprintf("%d", *p.FamilyID)
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", p.ID, p.Name, fam, p.Note, p.CreatedAt.Format("2006-01-02"))
		}
		w.Flush()
	case "rm":
		fs := flag.NewFlagSet("person rm", flag.ExitOnError)
		id := fs.Int64("id", 0, "person id")
		fs.Parse(rest)
		if *id == 0 {
			return errors.New("-id is required")
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		return s.DeletePerson(*id)
	default:
		return fmt.Errorf("unknown person subcommand: %s", sub)
	}
	return nil
}

// ---- category ----

func cmdCategory(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: suanpan category <add|list|rm> ...")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		fs := flag.NewFlagSet("category add", flag.ExitOnError)
		name := fs.String("name", "", "category name (required)")
		kind := fs.String("kind", "expense", "kind: income|expense")
		parent := fs.Int64("parent", 0, "parent category id")
		ownerStr := fs.String("owner", "", "owner kind:id (optional, blank = global)")
		icon := fs.String("icon", "", "emoji/icon")
		asJSON := fs.Bool("json", false, "output JSON")
		fs.Parse(rest)
		if *name == "" {
			return errors.New("-name is required")
		}
		oKind, oID, err := parseOwner(*ownerStr)
		if err != nil {
			return err
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		var pid *int64
		if *parent != 0 {
			pid = parent
		}
		var okP *string
		var oidP *int64
		if oKind != "" {
			okP = &oKind
			oidP = &oID
		}
		c, err := s.CreateCategory(store.Category{
			Name: *name, Kind: *kind, ParentID: pid,
			OwnerKind: okP, OwnerID: oidP, Icon: *icon,
		})
		if err != nil {
			return err
		}
		if *asJSON {
			return emitJSON(c)
		}
		fmt.Printf("category #%d %s (%s)\n", c.ID, c.Name, c.Kind)
	case "list":
		fs := flag.NewFlagSet("category list", flag.ExitOnError)
		kind := fs.String("kind", "", "filter by kind")
		ownerStr := fs.String("owner", "", "filter by owner (person:1)")
		asJSON := fs.Bool("json", false, "output JSON")
		fs.Parse(rest)
		oKind, oID, err := parseOwner(*ownerStr)
		if err != nil {
			return err
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		cs, err := s.ListCategories(*kind, oKind, oID)
		if err != nil {
			return err
		}
		if *asJSON {
			return emitJSON(cs)
		}
		w := tw()
		fmt.Fprintln(w, "ID\tNAME\tKIND\tICON\tSCOPE")
		for _, c := range cs {
			scope := "global"
			if c.OwnerKind != nil {
				scope = fmt.Sprintf("%s:%d", *c.OwnerKind, *c.OwnerID)
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", c.ID, c.Name, c.Kind, c.Icon, scope)
		}
		w.Flush()
	case "rm":
		fs := flag.NewFlagSet("category rm", flag.ExitOnError)
		id := fs.Int64("id", 0, "category id")
		fs.Parse(rest)
		if *id == 0 {
			return errors.New("-id is required")
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		return s.DeleteCategory(*id)
	default:
		return fmt.Errorf("unknown category subcommand: %s", sub)
	}
	return nil
}

// ---- txn ----

func cmdTxn(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: suanpan txn <add|list|rm> ...")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		fs := flag.NewFlagSet("txn add", flag.ExitOnError)
		amount := fs.String("amount", "", "amount decimal (required)")
		kind := fs.String("kind", "expense", "income|expense")
		category := fs.Int64("category", 0, "category id")
		categoryName := fs.String("category-name", "", "category name (resolved to id)")
		person := fs.Int64("person", 0, "person id")
		family := fs.Int64("family", 0, "family id")
		dateStr := fs.String("date", "", "YYYY-MM-DD (default now)")
		payee := fs.String("payee", "", "payee/merchant")
		note := fs.String("note", "", "note")
		tags := fs.String("tags", "", "comma-separated tags")
		currency := fs.String("currency", "CNY", "currency")
		asJSON := fs.Bool("json", false, "output JSON")
		fs.Parse(rest)
		if *amount == "" {
			return errors.New("-amount is required")
		}
		if *kind != "income" && *kind != "expense" {
			return errors.New("-kind must be income or expense")
		}
		if *person == 0 && *family == 0 {
			return errors.New("-person <id> or -family <id> is required (every txn must have an owner)")
		}
		amt, err := store.ParseAmount(*amount)
		if err != nil {
			return err
		}
		if amt <= 0 {
			return errors.New("amount must be positive")
		}
		when, err := parseDate(*dateStr)
		if err != nil {
			return err
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		var catID *int64
		if *category != 0 {
			catID = category
		} else if *categoryName != "" {
			c, err := s.FindCategoryByName(*categoryName, *kind)
			if err != nil {
				return err
			}
			if c == nil {
				return fmt.Errorf("no category matches %q (kind=%s)", *categoryName, *kind)
			}
			catID = &c.ID
		}
		var perID, famID *int64
		if *person != 0 {
			perID = person
		}
		if *family != 0 {
			famID = family
		}
		t, err := s.CreateTxn(store.Txn{
			OccurredAt: when, Kind: *kind, Amount: amt, Currency: *currency,
			CategoryID: catID, PersonID: perID, FamilyID: famID,
			Payee: *payee, Note: *note, Tags: *tags,
		})
		if err != nil {
			return err
		}
		if *asJSON {
			return emitJSON(t)
		}
		fmt.Printf("txn #%d %s %s %s on %s\n", t.ID, t.Kind, store.FormatAmount(t.Amount), t.Currency,
			t.OccurredAt.Format("2006-01-02"))
	case "list":
		fs := flag.NewFlagSet("txn list", flag.ExitOnError)
		asPerson := fs.Int64("as-person", 0, "caller identity for family scope (or $SUANPAN_AS_PERSON)")
		since := fs.String("since", "", "YYYY-MM-DD")
		until := fs.String("until", "", "YYYY-MM-DD")
		kind := fs.String("kind", "", "income|expense")
		person := fs.Int64("person", 0, "person id")
		family := fs.Int64("family", 0, "family id")
		category := fs.Int64("category", 0, "category id")
		search := fs.String("search", "", "substring match in payee/note")
		limit := fs.Int("limit", 50, "limit")
		asJSON := fs.Bool("json", false, "output JSON")
		fs.Parse(rest)
		scope, err := resolveScopePerson(*asPerson)
		if err != nil {
			return err
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		txns, err := s.ListTxns(store.TxnFilter{
			Since: *since, Until: *until, Kind: *kind,
			PersonID: *person, FamilyID: *family, CategoryID: *category,
			Search: *search, Limit: *limit, ScopePersonID: scope,
		})
		if err != nil {
			return err
		}
		if *asJSON {
			return emitJSON(txns)
		}
		w := tw()
		fmt.Fprintln(w, "ID\tDATE\tKIND\tAMOUNT\tCCY\tCAT\tPER\tPAYEE\tNOTE")
		for _, t := range txns {
			cat := "-"
			if t.CategoryID != nil {
				cat = strconv.FormatInt(*t.CategoryID, 10)
			}
			per := "-"
			if t.PersonID != nil {
				per = strconv.FormatInt(*t.PersonID, 10)
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				t.ID, t.OccurredAt.Format("2006-01-02"), t.Kind,
				store.FormatAmount(t.Amount), t.Currency,
				cat, per, t.Payee, truncate(t.Note, 20))
		}
		w.Flush()
	case "rm":
		fs := flag.NewFlagSet("txn rm", flag.ExitOnError)
		id := fs.Int64("id", 0, "txn id")
		fs.Parse(rest)
		if *id == 0 {
			return errors.New("-id is required")
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		return s.DeleteTxn(*id)
	default:
		return fmt.Errorf("unknown txn subcommand: %s", sub)
	}
	return nil
}

// ---- budget ----

func cmdBudget(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: suanpan budget <add|list|status|rm> ...")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		fs := flag.NewFlagSet("budget add", flag.ExitOnError)
		name := fs.String("name", "", "budget name (required)")
		period := fs.String("period", "monthly", "weekly|monthly|yearly")
		amount := fs.String("amount", "", "amount decimal (required)")
		currency := fs.String("currency", "CNY", "currency")
		category := fs.Int64("category", 0, "category id (optional)")
		ownerStr := fs.String("owner", "", "owner kind:id (required)")
		start := fs.String("start", "", "YYYY-MM-DD start (required)")
		end := fs.String("end", "", "YYYY-MM-DD end (optional)")
		asJSON := fs.Bool("json", false, "output JSON")
		fs.Parse(rest)
		if *name == "" || *amount == "" || *ownerStr == "" || *start == "" {
			return errors.New("-name, -amount, -owner, -start are required")
		}
		amt, err := store.ParseAmount(*amount)
		if err != nil {
			return err
		}
		kind, id, err := parseOwner(*ownerStr)
		if err != nil {
			return err
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		var catID *int64
		if *category != 0 {
			catID = category
		}
		b, err := s.CreateBudget(store.Budget{
			Name: *name, Period: *period, Amount: amt, Currency: *currency,
			CategoryID: catID, OwnerKind: kind, OwnerID: id, StartDate: *start, EndDate: *end,
		})
		if err != nil {
			return err
		}
		if *asJSON {
			return emitJSON(b)
		}
		fmt.Printf("budget #%d %s (%s %s %s)\n", b.ID, b.Name, b.Period, store.FormatAmount(b.Amount), b.Currency)
	case "list":
		fs := flag.NewFlagSet("budget list", flag.ExitOnError)
		asPerson := fs.Int64("as-person", 0, "caller identity for family scope (or $SUANPAN_AS_PERSON)")
		ownerStr := fs.String("owner", "", "further narrow by owner (kind:id)")
		asJSON := fs.Bool("json", false, "output JSON")
		fs.Parse(rest)
		scope, err := resolveScopePerson(*asPerson)
		if err != nil {
			return err
		}
		kind, id, err := parseOwner(*ownerStr)
		if err != nil {
			return err
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		bs, err := s.ListBudgets(kind, id, scope)
		if err != nil {
			return err
		}
		if *asJSON {
			return emitJSON(bs)
		}
		w := tw()
		fmt.Fprintln(w, "ID\tNAME\tPERIOD\tAMOUNT\tCCY\tOWNER\tCATEGORY\tSTART\tEND")
		for _, b := range bs {
			cat := "-"
			if b.CategoryID != nil {
				cat = strconv.FormatInt(*b.CategoryID, 10)
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s:%d\t%s\t%s\t%s\n",
				b.ID, b.Name, b.Period, store.FormatAmount(b.Amount), b.Currency,
				b.OwnerKind, b.OwnerID, cat, b.StartDate, b.EndDate)
		}
		w.Flush()
	case "status":
		fs := flag.NewFlagSet("budget status", flag.ExitOnError)
		asPerson := fs.Int64("as-person", 0, "caller identity for family scope (or $SUANPAN_AS_PERSON)")
		ownerStr := fs.String("owner", "", "further narrow by owner (kind:id)")
		dateStr := fs.String("date", "", "reference date (YYYY-MM-DD), default today")
		asJSON := fs.Bool("json", false, "output JSON")
		fs.Parse(rest)
		scope, err := resolveScopePerson(*asPerson)
		if err != nil {
			return err
		}
		kind, id, err := parseOwner(*ownerStr)
		if err != nil {
			return err
		}
		ref := time.Now()
		if *dateStr != "" {
			ref, err = parseDate(*dateStr)
			if err != nil {
				return err
			}
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		bs, err := s.ListBudgets(kind, id, scope)
		if err != nil {
			return err
		}
		var statuses []*store.BudgetStatus
		for _, b := range bs {
			st, err := s.BudgetStatusAt(b, ref)
			if err != nil {
				return err
			}
			statuses = append(statuses, st)
		}
		if *asJSON {
			return emitJSON(statuses)
		}
		w := tw()
		fmt.Fprintln(w, "ID\tNAME\tPERIOD\tSPENT/BUDGET\tPCT\tREMAIN\tWINDOW")
		for _, st := range statuses {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s / %s\t%d%%\t%s\t%s..%s\n",
				st.Budget.ID, st.Budget.Name, st.Budget.Period,
				store.FormatAmount(st.Spent), store.FormatAmount(st.Budget.Amount),
				st.Percent, store.FormatAmount(st.Remaining),
				st.Since, st.Until)
		}
		w.Flush()
	case "rm":
		fs := flag.NewFlagSet("budget rm", flag.ExitOnError)
		id := fs.Int64("id", 0, "budget id")
		fs.Parse(rest)
		if *id == 0 {
			return errors.New("-id is required")
		}
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		return s.DeleteBudget(*id)
	default:
		return fmt.Errorf("unknown budget subcommand: %s", sub)
	}
	return nil
}

// ---- report ----

func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	asPerson := fs.Int64("as-person", 0, "caller identity for family scope (or $SUANPAN_AS_PERSON)")
	since := fs.String("since", "", "YYYY-MM-DD")
	until := fs.String("until", "", "YYYY-MM-DD")
	person := fs.Int64("person", 0, "further narrow by person id")
	family := fs.Int64("family", 0, "further narrow by family id")
	asJSON := fs.Bool("json", false, "output JSON")
	fs.Parse(args)

	scope, err := resolveScopePerson(*asPerson)
	if err != nil {
		return err
	}

	// Default to current month if neither provided.
	if *since == "" && *until == "" {
		now := time.Now()
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		end := start.AddDate(0, 1, -1)
		*since = start.Format("2006-01-02")
		*until = end.Format("2006-01-02")
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	sum, err := s.Summarize(store.TxnFilter{
		Since: *since, Until: *until,
		PersonID: *person, FamilyID: *family,
		ScopePersonID: scope,
	})
	if err != nil {
		return err
	}
	if *asJSON {
		return emitJSON(sum)
	}
	fmt.Printf("Period: %s .. %s  (txns=%d)\n", sum.Since, sum.Until, sum.TxnCount)
	fmt.Printf("  Income : %s %s\n", store.FormatAmount(sum.Income), sum.Currency)
	fmt.Printf("  Expense: %s %s\n", store.FormatAmount(sum.Expense), sum.Currency)
	fmt.Printf("  Net    : %s %s\n", store.FormatAmount(sum.Net), sum.Currency)
	if len(sum.ByCategory) > 0 {
		fmt.Println("\nBy category:")
		w := tw()
		fmt.Fprintln(w, "  KIND\tCATEGORY\tAMOUNT")
		for _, c := range sum.ByCategory {
			fmt.Fprintf(w, "  %s\t%s\t%s\n", c.Kind, c.CategoryName, store.FormatAmount(c.Amount))
		}
		w.Flush()
	}
	return nil
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
