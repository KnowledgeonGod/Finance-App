package httpapi

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	emailPattern    = regexp.MustCompile(`^[^@\s]{1,64}@[^@\s]{1,190}\.[^@\s]{1,63}$`)
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
	uuidPattern     = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

func validEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(email) > 254 || !emailPattern.MatchString(email) {
		return "", errors.New("email is invalid")
	}
	return email, nil
}

func validPassword(password string) error {
	if utf8.RuneCountInString(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if len(password) > 72 {
		return errors.New("password must not exceed 72 bytes")
	}
	return nil
}

func validRegistrationPassword(password string) error {
	if err := validPassword(password); err != nil {
		return err
	}
	hasUppercase := false
	hasSpecial := false
	for _, char := range password {
		hasUppercase = hasUppercase || unicode.IsUpper(char)
		hasSpecial = hasSpecial || unicode.IsPunct(char) || unicode.IsSymbol(char)
	}
	if !hasUppercase {
		return errors.New("password must include an uppercase letter")
	}
	if !hasSpecial {
		return errors.New("password must include a special character")
	}
	return nil
}

func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if length := utf8.RuneCountInString(name); length == 0 || length > 80 {
		return "", errors.New("category name must be between 1 and 80 characters")
	}
	for _, char := range name {
		if unicode.IsControl(char) {
			return "", errors.New("category name cannot contain control characters")
		}
	}
	return name, nil
}

func validCurrency(currency string) (string, error) {
	if !currencyPattern.MatchString(currency) {
		return "", errors.New("currency must be a three-letter uppercase code")
	}
	return currency, nil
}

func validMonth(month string) (time.Time, error) {
	parsed, err := time.Parse("2006-01", month)
	if err != nil {
		return time.Time{}, errors.New("month must use YYYY-MM format")
	}
	return parsed.UTC(), nil
}

func validDate(date string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return time.Time{}, errors.New("date must use YYYY-MM-DD format")
	}
	return parsed, nil
}

func validUUID(id string) bool {
	return uuidPattern.MatchString(id)
}
