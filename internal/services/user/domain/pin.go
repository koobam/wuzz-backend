package domain

import "github.com/koobam/wuzz-backend/internal/pkg/apperr"

// PinLength is how many digits a sign-in PIN has.
const PinLength = 6

// ValidatePin accepts exactly six digits and refuses the ones anyone would try first: all the same digit (000000) and a
// straight run up or down (123456, 654321).
func ValidatePin(pin string) error {
	if len(pin) != PinLength {
		return apperr.Invalid("The PIN must be 6 digits")
	}
	for i := 0; i < len(pin); i++ {
		if pin[i] < '0' || pin[i] > '9' {
			return apperr.Invalid("The PIN must be 6 digits")
		}
	}
	if isRun(pin, 0) || isRun(pin, 1) || isRun(pin, -1) {
		return apperr.Invalid("Choose a PIN that is not easy to guess")
	}
	return nil
}

// isRun reports whether every digit is step away from the one before: 0 is a repeated digit, 1 counts up, -1 counts down.
func isRun(pin string, step int) bool {
	for i := 1; i < len(pin); i++ {
		if int(pin[i])-int(pin[i-1]) != step {
			return false
		}
	}
	return true
}
