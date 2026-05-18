package store

import "time"

type Family struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Currency  string    `json:"currency"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Person struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	FamilyID  *int64    `json:"family_id,omitempty"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Category struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"` // income|expense
	ParentID  *int64    `json:"parent_id,omitempty"`
	OwnerKind *string   `json:"owner_kind,omitempty"`
	OwnerID   *int64    `json:"owner_id,omitempty"`
	Icon      string    `json:"icon,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Txn struct {
	ID         int64     `json:"id"`
	OccurredAt time.Time `json:"occurred_at"`
	Kind       string    `json:"kind"` // income|expense
	Amount     int64     `json:"amount"`
	Currency   string    `json:"currency"`
	CategoryID *int64    `json:"category_id,omitempty"`
	PersonID   *int64    `json:"person_id,omitempty"`
	FamilyID   *int64    `json:"family_id,omitempty"`
	Payee      string    `json:"payee,omitempty"`
	Note       string    `json:"note,omitempty"`
	Tags       string    `json:"tags,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type Budget struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Period     string    `json:"period"`
	Amount     int64     `json:"amount"`
	Currency   string    `json:"currency"`
	CategoryID *int64    `json:"category_id,omitempty"`
	OwnerKind  string    `json:"owner_kind"`
	OwnerID    int64     `json:"owner_id"`
	StartDate  string    `json:"start_date"`
	EndDate    string    `json:"end_date,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// TxnFilter narrows ListTxns/Summarize. ScopePersonID is the security gate
// for reads: when set (>0), results are restricted to txns belonging to that
// person, to any sibling sharing the same family, or to the person's family
// ledger. Other families' data is invisible.
type TxnFilter struct {
	Since         string // YYYY-MM-DD inclusive
	Until         string // YYYY-MM-DD inclusive
	Kind          string // income|expense
	PersonID      int64
	FamilyID      int64
	CategoryID    int64
	Search        string // match payee/note
	Limit         int
	Offset        int
	ScopePersonID int64
}

type Summary struct {
	Since      string           `json:"since,omitempty"`
	Until      string           `json:"until,omitempty"`
	Income     int64            `json:"income"`
	Expense    int64            `json:"expense"`
	Net        int64            `json:"net"`
	Currency   string           `json:"currency"`
	ByCategory []CategoryAmount `json:"by_category,omitempty"`
	TxnCount   int              `json:"txn_count"`
}

type CategoryAmount struct {
	CategoryID   *int64 `json:"category_id,omitempty"`
	CategoryName string `json:"category_name"`
	Kind         string `json:"kind"`
	Amount       int64  `json:"amount"`
}
