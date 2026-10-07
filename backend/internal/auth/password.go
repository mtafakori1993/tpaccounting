package auth

import (
	"sync"

	"golang.org/x/crypto/bcrypt"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
)

const bcryptCost = 12

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	return string(b), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// ValidatePassword enforces the password policy, reporting errors on field.
func ValidatePassword(v *apperr.Validator, field, pw string) {
	n := len([]rune(pw))
	v.Check(n >= 8, field, "password_too_short")
	v.Check(len(pw) <= 72, field, "too_long") // bcrypt limit
}

var (
	dummyOnce sync.Once
	dummy     string
)

// dummyHash is compared against when the user does not exist, equalising response time.
func dummyHash() string {
	dummyOnce.Do(func() { dummy, _ = HashPassword("tp-dummy-password-for-timing") })
	return dummy
}
