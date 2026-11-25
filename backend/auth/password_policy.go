package auth

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"kubendt/settings"
)

const defaultPasswordMinLength = 8

// WeakPasswordError says why the policy refused a password.
type WeakPasswordError struct{ Reason string }

func (e *WeakPasswordError) Error() string { return e.Reason }

// PasswordMinLength is the least a chosen password may be long:
// KUBENDT_PASSWORD_MIN_LENGTH, or eight.
func PasswordMinLength() int {
	if n, err := strconv.Atoi(strings.TrimSpace(settings.Value(passwordMinLengthKey))); err == nil && n > 0 {
		return n
	}
	return defaultPasswordMinLength
}

const passwordMinLengthKey = "KUBENDT_PASSWORD_MIN_LENGTH"

func init() {
	settings.Register(settings.Setting{
		Key: passwordMinLengthKey, Group: settings.GroupPasswords, Label: "Shortest password",
		Unit: "characters", Default: strconv.Itoa(defaultPasswordMinLength),
		Hint:  "For passwords chosen from now on, not the ones already set.",
		Check: checkMinLengthSetting, Order: -1,
	})
}

// The floor keeps the portal from allowing a one-letter password, the ceiling
// from a length nobody can type.
func checkMinLengthSetting(v string) error {
	if v == "" {
		return nil
	}
	if n, err := strconv.Atoi(v); err != nil || n < 4 || n > 128 {
		return fmt.Errorf("a number of characters between 4 and 128")
	}
	return nil
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
