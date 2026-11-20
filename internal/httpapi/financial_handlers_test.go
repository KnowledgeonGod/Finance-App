package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v5"
)

const testUserID = "b3f2a610-5847-4e1f-b5ab-1fc407ac8169"

func handlerRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request.WithContext(context.WithValue(request.Context(), userIDKey, testUserID))
}

func TestCreateTransactionRejectsInvalidInputBeforeDatabaseAccess(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown transaction type", body: `{"kind":"transfer","amount_cents":100,"currency":"USD","occurred_on":"2026-01-01"}`},
		{name: "nonpositive amount", body: `{"kind":"expense","amount_cents":0,"currency":"USD","occurred_on":"2026-01-01"}`},
		{name: "invalid currency", body: `{"kind":"expense","amount_cents":100,"currency":"usd","occurred_on":"2026-01-01"}`},
		{name: "invalid category ID", body: `{"kind":"expense","amount_cents":100,"currency":"USD","occurred_on":"2026-01-01","category_id":"' OR 1=1 --"}`},
		{name: "invalid date", body: `{"kind":"expense","amount_cents":100,"currency":"USD","occurred_on":"2026-02-30"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			response := httptest.NewRecorder()
			server.createTransaction(response, handlerRequest(http.MethodPost, "/", test.body))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestCreateTransactionPersistsParameterizedData(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	date := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	createdAt := date.Add(time.Hour)
	const transactionID = "a1073f76-c152-4d18-80c2-a8766392e82f"
	pool.ExpectQuery(`INSERT INTO transactions`).
		WithArgs(pgxmock.AnyArg(), testUserID, pgxmock.AnyArg(), "expense", int64(2599), "USD", "Groceries", date).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "category_id", "kind", "amount_cents", "currency", "description", "occurred_on", "created_at",
		}).AddRow(transactionID, nil, "expense", int64(2599), "USD", "Groceries", date, createdAt))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	server.createTransaction(response, handlerRequest(http.MethodPost, "/",
		`{"kind":"expense","amount_cents":2599,"currency":"USD","description":"Groceries","occurred_on":"2026-11-18"}`))

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result transaction
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.ID != transactionID || result.AmountCents != 2599 || result.OccurredOn != "2026-11-18" {
		t.Fatalf("unexpected transaction response: %+v", result)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestUpdateTransactionScopesUpdateToAuthenticatedUser(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	date := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	transactionID := "a1073f76-c152-4d18-80c2-a8766392e82f"
	pool.ExpectQuery(`UPDATE transactions SET category_id = \$1, kind = \$2`).
		WithArgs(pgxmock.AnyArg(), "income", int64(9000), "USD", "Salary", date, transactionID, testUserID).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "category_id", "kind", "amount_cents", "currency", "description", "occurred_on", "created_at",
		}).AddRow(transactionID, nil, "income", int64(9000), "USD", "Salary", date, date))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := handlerRequest(http.MethodPut, "/"+transactionID,
		`{"kind":"income","amount_cents":9000,"currency":"USD","description":"Salary","occurred_on":"2026-11-18"}`)
	request.SetPathValue("id", transactionID)
	response := httptest.NewRecorder()
	server.updateTransaction(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestUpdateTransactionReturnsNotFoundForUnknownRecord(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	transactionID := "a1073f76-c152-4d18-80c2-a8766392e82f"
	pool.ExpectQuery(`UPDATE transactions SET category_id`).
		WithArgs(pgxmock.AnyArg(), "income", int64(9000), "USD", "", time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC), transactionID, testUserID).
		WillReturnError(pgx.ErrNoRows)

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := handlerRequest(http.MethodPut, "/"+transactionID,
		`{"kind":"income","amount_cents":9000,"currency":"USD","description":"","occurred_on":"2026-11-18"}`)
	request.SetPathValue("id", transactionID)
	response := httptest.NewRecorder()
	server.updateTransaction(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestCreateCategoryMapsDuplicateNameToConflict(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectQuery(`INSERT INTO categories`).
		WithArgs(pgxmock.AnyArg(), testUserID, "Food").
		WillReturnError(&pgconn.PgError{Code: "23505"})

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	server.createCategory(response, handlerRequest(http.MethodPost, "/", `{"name":" Food "}`))

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestCreateCategoryReturnsCreatedCategory(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	createdAt := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)
	pool.ExpectQuery(`INSERT INTO categories`).
		WithArgs(pgxmock.AnyArg(), testUserID, "Food").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "created_at"}).
			AddRow("a1073f76-c152-4d18-80c2-a8766392e82f", "Food", createdAt))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	server.createCategory(response, handlerRequest(http.MethodPost, "/", `{"name":" Food "}`))

	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"name":"Food"`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestListCategoriesReturnsAccountCategories(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectQuery(`SELECT id, name, created_at FROM categories WHERE user_id = \$1`).
		WithArgs(testUserID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "created_at"}).
			AddRow("a1073f76-c152-4d18-80c2-a8766392e82f", "Food", time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	server.listCategories(response, handlerRequest(http.MethodGet, "/", ""))

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"Food"`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestListTransactionsRejectsInjectionInFilter(t *testing.T) {
	server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	request := handlerRequest(http.MethodGet, "/?kind=expense%27%20OR%201%3D1", "")
	server.listTransactions(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestDeleteTransactionScopesByUserAndReturnsNotFound(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectExec(`DELETE FROM transactions WHERE id = \$1 AND user_id = \$2`).
		WithArgs("a1073f76-c152-4d18-80c2-a8766392e82f", testUserID).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	request := handlerRequest(http.MethodDelete, "/a1073f76-c152-4d18-80c2-a8766392e82f", "")
	request.SetPathValue("id", "a1073f76-c152-4d18-80c2-a8766392e82f")
	server.deleteTransaction(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestDeleteTransactionReturnsNoContentForOwnedRecord(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	transactionID := "a1073f76-c152-4d18-80c2-a8766392e82f"
	pool.ExpectExec(`DELETE FROM transactions WHERE id = \$1 AND user_id = \$2`).
		WithArgs(transactionID, testUserID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := handlerRequest(http.MethodDelete, "/"+transactionID, "")
	request.SetPathValue("id", transactionID)
	response := httptest.NewRecorder()
	server.deleteTransaction(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestMonthlyReportRejectsInvalidMonthWithoutDatabaseAccess(t *testing.T) {
	server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	server.monthlyReport(response, handlerRequest(http.MethodGet, "/?month=2026-13", ""))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestCreateBudgetRejectsInvalidInputWithoutDatabaseAccess(t *testing.T) {
	tests := []string{
		`{"month":"2026-13","amount_cents":5000,"currency":"USD"}`,
		`{"month":"2026-01","amount_cents":0,"currency":"USD"}`,
		`{"month":"2026-01","amount_cents":5000,"currency":"usd"}`,
		`{"month":"2026-01","amount_cents":5000,"currency":"USD","category_id":"injected' OR 1=1"}`,
	}
	for _, body := range tests {
		server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		response := httptest.NewRecorder()
		server.createBudget(response, handlerRequest(http.MethodPost, "/", body))
		if response.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want %d", body, response.Code, http.StatusBadRequest)
		}
	}
}

func TestCreateBudgetPersistsBudget(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	pool.ExpectQuery(`INSERT INTO budgets`).
		WithArgs(pgxmock.AnyArg(), testUserID, pgxmock.AnyArg(), month, int64(20000), "USD").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "category_id", "month", "amount_cents", "currency", "created_at",
		}).AddRow("a1073f76-c152-4d18-80c2-a8766392e82f", nil, month, int64(20000), "USD", month))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	server.createBudget(response, handlerRequest(http.MethodPost, "/",
		`{"month":"2026-11","amount_cents":20000,"currency":"USD"}`))

	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"month":"2026-11"`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestCreateBudgetMapsDuplicateScopeToConflict(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	pool.ExpectQuery(`INSERT INTO budgets`).
		WithArgs(pgxmock.AnyArg(), testUserID, pgxmock.AnyArg(), month, int64(20000), "USD").
		WillReturnError(&pgconn.PgError{Code: "23505"})

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	server.createBudget(response, handlerRequest(http.MethodPost, "/",
		`{"month":"2026-11","amount_cents":20000,"currency":"USD"}`))

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestDatabaseBackedReadHandlersReturnGenericServerErrors(t *testing.T) {
	tests := []struct {
		name        string
		expectedSQL string
		args        []any
		call        func(*Server, *httptest.ResponseRecorder)
	}{
		{
			name:        "categories",
			expectedSQL: `SELECT id, name, created_at FROM categories`,
			args:        []any{testUserID},
			call: func(server *Server, response *httptest.ResponseRecorder) {
				server.listCategories(response, handlerRequest(http.MethodGet, "/", ""))
			},
		},
		{
			name:        "transactions",
			expectedSQL: `SELECT id, category_id, kind, amount_cents`,
			args:        []any{testUserID, 50, 0},
			call: func(server *Server, response *httptest.ResponseRecorder) {
				server.listTransactions(response, handlerRequest(http.MethodGet, "/", ""))
			},
		},
		{
			name:        "monthly report",
			expectedSQL: `SELECT kind, currency, count`,
			args:        []any{testUserID, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)},
			call: func(server *Server, response *httptest.ResponseRecorder) {
				server.monthlyReport(response, handlerRequest(http.MethodGet, "/?month=2026-11", ""))
			},
		},
		{
			name:        "budgets",
			expectedSQL: `SELECT id, category_id, month, amount_cents, currency, created_at`,
			args:        []any{testUserID},
			call: func(server *Server, response *httptest.ResponseRecorder) {
				server.listBudgets(response, handlerRequest(http.MethodGet, "/", ""))
			},
		},
		{
			name:        "budget alerts",
			expectedSQL: `SELECT b.id, b.category_id, b.month`,
			args:        []any{testUserID, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)},
			call: func(server *Server, response *httptest.ResponseRecorder) {
				server.budgetAlerts(response, handlerRequest(http.MethodGet, "/?month=2026-11", ""))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, err := pgxmock.NewPool()
			if err != nil {
				t.Fatalf("create PostgreSQL mock: %v", err)
			}
			defer pool.Close()
			pool.ExpectQuery(test.expectedSQL).WithArgs(test.args...).
				WillReturnError(errors.New("simulated database failure"))
			server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			response := httptest.NewRecorder()
			test.call(server, response)
			if response.Code != http.StatusInternalServerError ||
				strings.Contains(response.Body.String(), "simulated database failure") {
				t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
			}
			if err := pool.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet PostgreSQL expectations: %v", err)
			}
		})
	}
}

func TestListBudgetsReturnsStoredBudgets(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	pool.ExpectQuery(`FROM budgets WHERE user_id = \$1`).
		WithArgs(testUserID).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "category_id", "month", "amount_cents", "currency", "created_at",
		}).AddRow("a1073f76-c152-4d18-80c2-a8766392e82f", nil, month, int64(20000), "USD", month))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	server.listBudgets(response, handlerRequest(http.MethodGet, "/", ""))

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"amount_cents":20000`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestListTransactionsBindsFiltersAndPagination(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	date := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	categoryUUID := pgtype.UUID{
		Bytes: [16]byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0},
		Valid: true,
	}
	pool.ExpectQuery(`FROM transactions WHERE user_id = \$1 AND kind = \$2 AND occurred_on >= \$3 AND occurred_on <= \$4\s+ORDER BY occurred_on DESC, id LIMIT \$5 OFFSET \$6`).
		WithArgs(testUserID, "expense", date, date.AddDate(0, 1, 0), 10, 20).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "category_id", "kind", "amount_cents", "currency", "description", "occurred_on", "created_at",
		}).AddRow("a1073f76-c152-4d18-80c2-a8766392e82f", categoryUUID, "expense",
			int64(500), "USD", "Lunch", date, date.Add(time.Hour)))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := handlerRequest(http.MethodGet,
		"/?kind=expense&start_date=2026-11-01&end_date=2026-12-01&limit=10&offset=20", "")
	response := httptest.NewRecorder()
	server.listTransactions(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result []transaction
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result) != 1 || result[0].AmountCents != 500 || result[0].CategoryID == nil {
		t.Fatalf("unexpected transactions: %+v", result)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestMonthlyReportAggregatesByCurrencyAndKind(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	pool.ExpectQuery(`GROUP BY kind, currency ORDER BY currency, kind`).
		WithArgs(testUserID, month, month.AddDate(0, 1, 0)).
		WillReturnRows(pgxmock.NewRows([]string{"kind", "currency", "count", "amount_cents"}).
			AddRow("expense", "USD", int64(4), int64(12500)).
			AddRow("income", "USD", int64(2), int64(50000)))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	server.monthlyReport(response, handlerRequest(http.MethodGet, "/?month=2026-11", ""))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Month  string        `json:"month"`
		Totals []reportTotal `json:"totals"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Month != "2026-11" || len(result.Totals) != 2 || result.Totals[0].AmountCents != 12500 {
		t.Fatalf("unexpected report: %+v", result)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestBudgetAlertsCalculateThresholdAndSpentAmount(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	pool.ExpectQuery(`FROM budgets b`).
		WithArgs(testUserID, month).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "category_id", "month", "currency", "amount_cents", "spent_cents",
		}).AddRow("a1073f76-c152-4d18-80c2-a8766392e82f", nil, month, "USD", int64(10000), int64(8500)))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	server.budgetAlerts(response, handlerRequest(http.MethodGet, "/?month=2026-11", ""))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result []budgetAlert
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result) != 1 || result[0].SpentCents != 8500 || result[0].PercentSpent != 85 || !result[0].Alert {
		t.Fatalf("unexpected budget alert: %+v", result)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}
