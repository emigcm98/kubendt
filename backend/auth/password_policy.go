package auth

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

const defaultPasswordMinLength = 8

// WeakPasswordError says why the policy refused a password.
type WeakPasswordError struct{ Reason string }

func (e *WeakPasswordError) Error() string { return e.Reason }

// PasswordMinLength is the least a chosen password may be long:
// KUBENDT_PASSWORD_MIN_LENGTH, or eight.
func PasswordMinLength() int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("KUBENDT_PASSWORD_MIN_LENGTH"))); err == nil && n > 0 {
		return n
	}
	return defaultPasswordMinLength
}

// checkPassword is the policy on a password somebody chooses. Generated ones
// and the owner's from the environment do not pass through it.
func checkPassword(name, password string) error {
	if n := PasswordMinLength(); utf8.RuneCountInString(password) < n {
		return &WeakPasswordError{fmt.Sprintf("The password needs at least %d characters.", n)}
	}
	if strings.EqualFold(password, name) {
		return &WeakPasswordError{"The password cannot be the account's name."}
	}
	return nil
}
