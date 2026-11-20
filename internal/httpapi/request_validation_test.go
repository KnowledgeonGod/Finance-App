package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEmailValidationNormalizesAndRejectsInvalidAddresses(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: " Person@Example.com ", want: "person@example.com"},
		{input: "user+tag@example.co", want: "user+tag@example.co"},
		{input: "missing-at.example", wantErr: true},
		{input: "user @example.com", wantErr: true},
		{input: strings.Repeat("x", 255) + "@example.com", wantErr: true},
	}
	for _, test := range tests {
		got, err := validEmail(test.input)
		if (err != nil) != test.wantErr {
			t.Errorf("validEmail(%q) error = %v, wantErr %v", test.input, err, test.wantErr)
		}
		if err == nil && got != test.want {
			t.Errorf("validEmail(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestPasswordValidationEnforcesCharacterAndBcryptByteBounds(t *testing.T) {
	for _, test := range []struct {
		password string
		wantErr  bool
	}{
		{password: "1234567", wantErr: true},
		{password: "12345678"},
		{password: strings.Repeat("a", 72)},
		{password: strings.Repeat("a", 73), wantErr: true},
		{password: strings.Repeat("界", 8)},
		{password: strings.Repeat("界", 25), wantErr: true},
	} {
		if err := validPassword(test.password); (err != nil) != test.wantErr {
			t.Errorf("validPassword(%d bytes) error = %v, wantErr %v",
				len(test.password), err, test.wantErr)
		}
	}
}

func TestRegistrationPasswordRequiresUppercaseAndSpecialCharacter(t *testing.T) {
	for _, test := range []struct {
		password string
		wantErr  bool
	}{
		{password: "password1!", wantErr: true},
		{password: "Password1", wantErr: true},
		{password: "Password1!"},
		{password: "界界界界界界界A!"},
		{password: strings.Repeat("界", 24) + "A!", wantErr: true},
	} {
		if err := validRegistrationPassword(test.password); (err != nil) != test.wantErr {
			t.Errorf("validRegistrationPassword(%q) error = %v, wantErr %v",
				test.password, err, test.wantErr)
		}
	}
}

func TestCategoryNameValidationTrimsAndBoundsCharacters(t *testing.T) {
	name, err := validName("  Food  ")
	if err != nil || name != "Food" {
		t.Fatalf("validName() = %q, %v; want Food", name, err)
	}
	if _, err := validName(strings.Repeat("界", 81)); err == nil {
		t.Fatal("validName accepted more than 80 Unicode characters")
	}
	for _, invalid := range []string{"", " \t ", "Bad\nName"} {
		if _, err := validName(invalid); err == nil {
			t.Errorf("validName accepted %q", invalid)
		}
	}
}

func TestCurrencyValidationRequiresThreeUppercaseLetters(t *testing.T) {
	if got, err := validCurrency("USD"); err != nil || got != "USD" {
		t.Fatalf("validCurrency(USD) = %q, %v", got, err)
	}
	for _, invalid := range []string{"usd", "US", "USDD", "U$D", "123"} {
		if _, err := validCurrency(invalid); err == nil {
			t.Errorf("validCurrency accepted %q", invalid)
		}
	}
}

func TestDateAndMonthValidationRejectInvalidCalendarValues(t *testing.T) {
	date, err := validDate("2024-02-29")
	if err != nil || date.Format("2006-01-02") != "2024-02-29" {
		t.Fatalf("validDate leap day = %v, %v", date, err)
	}
	for _, invalid := range []string{"2023-02-29", "2024-13-01", "2024-01-1", "2024-01-01x"} {
		if _, err := validDate(invalid); err == nil {
			t.Errorf("validDate accepted %q", invalid)
		}
	}
	month, err := validMonth("2024-02")
	if err != nil || month.Format("2006-01") != "2024-02" {
		t.Fatalf("validMonth = %v, %v", month, err)
	}
	for _, invalid := range []string{"2024-13", "24-02", "2024-02-01"} {
		if _, err := validMonth(invalid); err == nil {
			t.Errorf("validMonth accepted %q", invalid)
		}
	}
}

func TestUUIDValidationRequiresCanonicalVersionedUUID(t *testing.T) {
	if !validUUID("b3f2a610-5847-4e1f-b5ab-1fc407ac8169") {
		t.Fatal("validUUID rejected a version 4 UUID")
	}
	for _, invalid := range []string{
		"",
		"not-a-uuid",
		"00000000-0000-0000-0000-000000000000",
		"b3f2a61058474e1fb5ab1fc407ac8169",
	} {
		if validUUID(invalid) {
			t.Errorf("validUUID accepted %q", invalid)
		}
	}
}

func TestPaginationDefaultsAndEnforcesBounds(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantLimit  int
		wantOffset int
		wantErr    bool
	}{
		{name: "defaults", wantLimit: 50},
		{name: "explicit values", query: "?limit=100&offset=1000000", wantLimit: 100, wantOffset: 1000000},
		{name: "zero limit", query: "?limit=0", wantErr: true},
		{name: "excessive limit", query: "?limit=101", wantErr: true},
		{name: "negative offset", query: "?offset=-1", wantErr: true},
		{name: "excessive offset", query: "?offset=1000001", wantErr: true},
		{name: "nonnumeric", query: "?limit=ten", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/api/v1/transactions"+test.query, nil)
			limit, offset, err := parseLimitOffset(request)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseLimitOffset error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && (limit != test.wantLimit || offset != test.wantOffset) {
				t.Fatalf("parseLimitOffset = %d/%d, want %d/%d", limit, offset, test.wantLimit, test.wantOffset)
			}
		})
	}
}

func TestValidMonthReturnsUTCFirstOfMonth(t *testing.T) {
	month, err := validMonth("2026-11")
	if err != nil {
		t.Fatal(err)
	}
	if month.Location() != time.UTC || month.Day() != 1 {
		t.Fatalf("validMonth returned %v, want UTC first day", month)
	}
}
