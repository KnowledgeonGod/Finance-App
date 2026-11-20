package httpapi

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type category struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type transaction struct {
	ID          string    `json:"id"`
	CategoryID  *string   `json:"category_id"`
	Kind        string    `json:"kind"`
	AmountCents int64     `json:"amount_cents"`
	Currency    string    `json:"currency"`
	Description string    `json:"description"`
	OccurredOn  string    `json:"occurred_on"`
	CreatedAt   time.Time `json:"created_at"`
}

type transactionInput struct {
	CategoryID  *string `json:"category_id"`
	Kind        string  `json:"kind"`
	AmountCents int64   `json:"amount_cents"`
	Currency    string  `json:"currency"`
	Description string  `json:"description"`
	OccurredOn  string  `json:"occurred_on"`
}

type budget struct {
	ID          string    `json:"id"`
	CategoryID  *string   `json:"category_id"`
	Month       string    `json:"month"`
	AmountCents int64     `json:"amount_cents"`
	Currency    string    `json:"currency"`
	CreatedAt   time.Time `json:"created_at"`
}

type budgetInput struct {
	CategoryID  *string `json:"category_id"`
	Month       string  `json:"month"`
	AmountCents int64   `json:"amount_cents"`
	Currency    string  `json:"currency"`
}

func (s *Server) listCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(),
		`SELECT id, name, created_at FROM categories WHERE user_id = $1 ORDER BY name, id`,
		userID(r))
	if err != nil {
		s.internalError(w, "list categories", err)
		return
	}
	defer rows.Close()
	items := make([]category, 0)
	for rows.Next() {
		var item category
		if err := rows.Scan(&item.ID, &item.Name, &item.CreatedAt); err != nil {
			s.internalError(w, "read category", err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, "iterate categories", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createCategory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		decodeError(w, err)
		return
	}
	name, err := validName(input.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := newUUID()
	if err != nil {
		s.internalError(w, "generate category ID", err)
		return
	}
	var item category
	err = s.db.QueryRow(r.Context(),
		`INSERT INTO categories (id, user_id, name) VALUES ($1, $2, $3)
		 RETURNING id, name, created_at`,
		id, userID(r), name,
	).Scan(&item.ID, &item.Name, &item.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "category already exists")
			return
		}
		s.internalError(w, "create category", err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) createTransaction(w http.ResponseWriter, r *http.Request) {
	s.saveTransaction(w, r, false)
}

func (s *Server) updateTransaction(w http.ResponseWriter, r *http.Request) {
	if !validUUID(r.PathValue("id")) {
		writeError(w, http.StatusBadRequest, "transaction ID is invalid")
		return
	}
	s.saveTransaction(w, r, true)
}

func (s *Server) saveTransaction(w http.ResponseWriter, r *http.Request, update bool) {
	var input transactionInput
	if err := decodeJSON(w, r, &input); err != nil {
		decodeError(w, err)
		return
	}
	if input.Kind != "income" && input.Kind != "expense" {
		writeError(w, http.StatusBadRequest, "kind must be income or expense")
		return
	}
	if input.AmountCents <= 0 {
		writeError(w, http.StatusBadRequest, "amount_cents must be positive")
		return
	}
	currency, err := validCurrency(input.Currency)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if utf8.RuneCountInString(input.Description) > 500 {
		writeError(w, http.StatusBadRequest, "description must be at most 500 characters")
		return
	}
	if input.CategoryID != nil && !validUUID(*input.CategoryID) {
		writeError(w, http.StatusBadRequest, "category_id is invalid")
		return
	}
	occurredOn, err := validDate(input.OccurredOn)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	if !update {
		id, err = newUUID()
		if err != nil {
			s.internalError(w, "generate transaction ID", err)
			return
		}
	}
	var item transaction
	var categoryID pgtype.UUID
	var query string
	if update {
		query = `UPDATE transactions SET category_id = $1, kind = $2, amount_cents = $3,
			currency = $4, description = $5, occurred_on = $6
			WHERE id = $7 AND user_id = $8
			RETURNING id, category_id, kind, amount_cents, currency, description, occurred_on, created_at`
		err = s.db.QueryRow(r.Context(), query,
			input.CategoryID, input.Kind, input.AmountCents, currency, input.Description, occurredOn,
			id, userID(r),
		).Scan(&item.ID, &categoryID, &item.Kind, &item.AmountCents, &item.Currency,
			&item.Description, &occurredOn, &item.CreatedAt)
	} else {
		query = `INSERT INTO transactions
			(id, user_id, category_id, kind, amount_cents, currency, description, occurred_on)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id, category_id, kind, amount_cents, currency, description, occurred_on, created_at`
		err = s.db.QueryRow(r.Context(), query,
			id, userID(r), input.CategoryID, input.Kind, input.AmountCents,
			currency, input.Description, occurredOn,
		).Scan(&item.ID, &categoryID, &item.Kind, &item.AmountCents, &item.Currency,
			&item.Description, &occurredOn, &item.CreatedAt)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) && update {
			writeError(w, http.StatusNotFound, "transaction not found")
			return
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			writeError(w, http.StatusBadRequest, "category not found")
			return
		}
		s.internalError(w, "save transaction", err)
		return
	}
	item.CategoryID = nullableUUID(categoryID)
	item.OccurredOn = occurredOn.Format("2006-01-02")
	status := http.StatusCreated
	if update {
		status = http.StatusOK
	}
	writeJSON(w, status, item)
}

func (s *Server) listTransactions(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parseLimitOffset(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	conditions := []string{"user_id = $1"}
	args := []any{userID(r)}
	addFilter := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	query := r.URL.Query()
	if kind := query.Get("kind"); kind != "" {
		if kind != "income" && kind != "expense" {
			writeError(w, http.StatusBadRequest, "kind must be income or expense")
			return
		}
		addFilter("kind = $%d", kind)
	}
	if categoryID := query.Get("category_id"); categoryID != "" {
		if !validUUID(categoryID) {
			writeError(w, http.StatusBadRequest, "category_id is invalid")
			return
		}
		addFilter("category_id = $%d", categoryID)
	}
	if raw := query.Get("start_date"); raw != "" {
		date, err := validDate(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		addFilter("occurred_on >= $%d", date)
	}
	if raw := query.Get("end_date"); raw != "" {
		date, err := validDate(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		addFilter("occurred_on <= $%d", date)
	}
	args = append(args, limit, offset)
	sql := `SELECT id, category_id, kind, amount_cents, currency, description, occurred_on, created_at
		FROM transactions WHERE ` + strings.Join(conditions, " AND ") +
		fmt.Sprintf(" ORDER BY occurred_on DESC, id LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := s.db.Query(r.Context(), sql, args...)
	if err != nil {
		s.internalError(w, "list transactions", err)
		return
	}
	defer rows.Close()
	items := make([]transaction, 0)
	for rows.Next() {
		var item transaction
		var categoryID pgtype.UUID
		var occurredOn time.Time
		if err := rows.Scan(&item.ID, &categoryID, &item.Kind, &item.AmountCents, &item.Currency,
			&item.Description, &occurredOn, &item.CreatedAt); err != nil {
			s.internalError(w, "read transaction", err)
			return
		}
		item.CategoryID = nullableUUID(categoryID)
		item.OccurredOn = occurredOn.Format("2006-01-02")
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, "iterate transactions", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) deleteTransaction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validUUID(id) {
		writeError(w, http.StatusBadRequest, "transaction ID is invalid")
		return
	}
	result, err := s.db.Exec(r.Context(),
		`DELETE FROM transactions WHERE id = $1 AND user_id = $2`, id, userID(r))
	if err != nil {
		s.internalError(w, "delete transaction", err)
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "transaction not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type reportTotal struct {
	Kind        string `json:"kind"`
	Currency    string `json:"currency"`
	Count       int64  `json:"count"`
	AmountCents int64  `json:"amount_cents"`
}

func (s *Server) monthlyReport(w http.ResponseWriter, r *http.Request) {
	month, err := validMonth(r.URL.Query().Get("month"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	nextMonth := month.AddDate(0, 1, 0)
	rows, err := s.db.Query(r.Context(),
		`SELECT kind, currency, count(*), sum(amount_cents)::bigint
		 FROM transactions
		 WHERE user_id = $1 AND occurred_on >= $2 AND occurred_on < $3
		 GROUP BY kind, currency ORDER BY currency, kind`,
		userID(r), month, nextMonth)
	if err != nil {
		s.internalError(w, "query monthly report", err)
		return
	}
	defer rows.Close()
	totals := make([]reportTotal, 0)
	for rows.Next() {
		var total reportTotal
		if err := rows.Scan(&total.Kind, &total.Currency, &total.Count, &total.AmountCents); err != nil {
			s.internalError(w, "read monthly report", err)
			return
		}
		totals = append(totals, total)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, "iterate monthly report", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"month": month.Format("2006-01"), "totals": totals})
}

func (s *Server) listBudgets(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(),
		`SELECT id, category_id, month, amount_cents, currency, created_at
		 FROM budgets WHERE user_id = $1 ORDER BY month DESC, currency, id`,
		userID(r))
	if err != nil {
		s.internalError(w, "list budgets", err)
		return
	}
	defer rows.Close()
	items := make([]budget, 0)
	for rows.Next() {
		var item budget
		var categoryID pgtype.UUID
		var month time.Time
		if err := rows.Scan(&item.ID, &categoryID, &month, &item.AmountCents, &item.Currency, &item.CreatedAt); err != nil {
			s.internalError(w, "read budget", err)
			return
		}
		item.CategoryID = nullableUUID(categoryID)
		item.Month = month.Format("2006-01")
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, "iterate budgets", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createBudget(w http.ResponseWriter, r *http.Request) {
	var input budgetInput
	if err := decodeJSON(w, r, &input); err != nil {
		decodeError(w, err)
		return
	}
	month, err := validMonth(input.Month)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.AmountCents <= 0 {
		writeError(w, http.StatusBadRequest, "amount_cents must be positive")
		return
	}
	currency, err := validCurrency(input.Currency)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.CategoryID != nil && !validUUID(*input.CategoryID) {
		writeError(w, http.StatusBadRequest, "category_id is invalid")
		return
	}
	id, err := newUUID()
	if err != nil {
		s.internalError(w, "generate budget ID", err)
		return
	}
	var item budget
	var categoryID pgtype.UUID
	var storedMonth time.Time
	err = s.db.QueryRow(r.Context(),
		`INSERT INTO budgets (id, user_id, category_id, month, amount_cents, currency)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, category_id, month, amount_cents, currency, created_at`,
		id, userID(r), input.CategoryID, month, input.AmountCents, currency,
	).Scan(&item.ID, &categoryID, &storedMonth, &item.AmountCents, &item.Currency, &item.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "budget already exists for this month and category")
			return
		}
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			writeError(w, http.StatusBadRequest, "category not found")
			return
		}
		s.internalError(w, "create budget", err)
		return
	}
	item.CategoryID = nullableUUID(categoryID)
	item.Month = storedMonth.Format("2006-01")
	writeJSON(w, http.StatusCreated, item)
}

type budgetAlert struct {
	BudgetID     string  `json:"budget_id"`
	CategoryID   *string `json:"category_id"`
	Month        string  `json:"month"`
	Currency     string  `json:"currency"`
	AmountCents  int64   `json:"amount_cents"`
	SpentCents   int64   `json:"spent_cents"`
	PercentSpent float64 `json:"percent_spent"`
	Alert        bool    `json:"alert"`
}

func (s *Server) budgetAlerts(w http.ResponseWriter, r *http.Request) {
	monthText := r.URL.Query().Get("month")
	if monthText == "" {
		monthText = time.Now().UTC().Format("2006-01")
	}
	month, err := validMonth(monthText)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.db.Query(r.Context(),
		`SELECT b.id, b.category_id, b.month, b.currency, b.amount_cents,
			COALESCE(sum(t.amount_cents), 0)::bigint
		 FROM budgets b
		 LEFT JOIN transactions t
		   ON t.user_id = b.user_id AND t.kind = 'expense' AND t.currency = b.currency
		  AND t.occurred_on >= b.month AND t.occurred_on < b.month + INTERVAL '1 month'
		  AND (b.category_id IS NULL OR t.category_id = b.category_id)
		 WHERE b.user_id = $1 AND b.month = $2
		 GROUP BY b.id, b.category_id, b.month, b.currency, b.amount_cents
		 ORDER BY b.currency, b.id`,
		userID(r), month)
	if err != nil {
		s.internalError(w, "query budget alerts", err)
		return
	}
	defer rows.Close()
	alerts := make([]budgetAlert, 0)
	for rows.Next() {
		var item budgetAlert
		var categoryID pgtype.UUID
		var storedMonth time.Time
		if err := rows.Scan(&item.BudgetID, &categoryID, &storedMonth,
			&item.Currency, &item.AmountCents, &item.SpentCents); err != nil {
			s.internalError(w, "read budget alert", err)
			return
		}
		item.CategoryID = nullableUUID(categoryID)
		item.Month = storedMonth.Format("2006-01")
		item.PercentSpent = float64(item.SpentCents) / float64(item.AmountCents) * 100
		item.Alert = item.PercentSpent >= 80
		alerts = append(alerts, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, "iterate budget alerts", err)
		return
	}
	writeJSON(w, http.StatusOK, alerts)
}

func nullableUUID(value pgtype.UUID) *string {
	if !value.Valid {
		return nil
	}
	encoded := hex.EncodeToString(value.Bytes[:])
	id := encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
	return &id
}
