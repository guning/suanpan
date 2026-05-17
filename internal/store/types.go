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

type Account struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Type           string    `json:"type"` // cash|bank|credit|investment|virtual
	Currency       string    `json:"currency"`
	InitialBalance int64     `json:"initial_balance"` // minor units
	OwnerKind      string    `json:"owner_kind"`      // person|family
	OwnerID        int64     `json:"owner_id"`
	Archived       bool      `json:"archived"`
	Note           string    `json:"note,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type Category struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"` // income|expense|transfer
	ParentID  *int64    `json:"parent_id,omitempty"`
	OwnerKind *string   `json:"owner_kind,omitempty"`
	OwnerID   *int64    `json:"owner_id,omitempty"`
	Icon      string    `json:"icon,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Txn struct {
	ID               int64     `json:"id"`
	OccurredAt       time.Time `json:"occurred_at"`
	Kind             string    `json:"kind"` // income|expense|transfer
	Amount           int64     `json:"amount"`
	Currency         string    `json:"currency"`
	AccountID        int64     `json:"account_id"`
	CounterAccountID *int64    `json:"counter_account_id,omitempty"`
	CategoryID       *int64    `json:"category_id,omitempty"`
	PersonID         *int64    `json:"person_id,omitempty"`
	FamilyID         *int64    `json:"family_id,omitempty"`
	Payee            string    `json:"payee,omitempty"`
	Note             string    `json:"note,omitempty"`
	Tags             string    `json:"tags,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
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

type TxnFilter struct {
	Since      string // YYYY-MM-DD inclusive
	Until      string // YYYY-MM-DD inclusive
	Kind       string // income|expense|transfer
	AccountID  int64
	PersonID   int64
	FamilyID   int64
	CategoryID int64
	Search     string // match payee/note
	Limit      int
	Offset     int
}

type Summary struct {
	Since        string           `json:"since,omitempty"`
	Until        string           `json:"until,omitempty"`
	Income       int64            `json:"income"`
	Expense      int64            `json:"expense"`
	Net          int64            `json:"net"`
	Currency     string           `json:"currency"`
	ByCategory   []CategoryAmount `json:"by_category,omitempty"`
	ByAccount    []AccountAmount  `json:"by_account,omitempty"`
	TxnCount     int              `json:"txn_count"`
}

type CategoryAmount struct {
	CategoryID   *int64 `json:"category_id,omitempty"`
	CategoryName string `json:"category_name"`
	Kind         string `json:"kind"`
	Amount       int64  `json:"amount"`
}

type AccountAmount struct {
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	Income      int64  `json:"income"`
	Expense     int64  `json:"expense"`
	Net         int64  `json:"net"`
}

type AccountBalance struct {
	Account Account `json:"account"`
	Balance int64   `json:"balance"` // minor units
}
