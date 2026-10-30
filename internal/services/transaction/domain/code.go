package domain

import (
	"crypto/rand"
	"regexp"
	"time"
)

// An order's code is the name people use for it: TRX-DDMMYYYY-XXXX, for example TRX-25102026-K7M2. The date is the day the order
// was created in Asia/Jakarta; the four characters are random, from an alphabet with no 0, O, 1 or I so that nobody mistakes one
// for another when reading it out.
const CodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

var codePattern = regexp.MustCompile(`^TRX-(\d{8})-([ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{4})$`)

// jakarta is Asia/Jakarta: UTC+7 all year, with no daylight saving, so a fixed zone is exact and needs no time zone database.
var jakarta = time.FixedZone("WIB", 7*60*60)

// ValidCode reports whether s has the shape of an order code and its date is a real calendar day (31022026 is not).
func ValidCode(s string) bool {
	m := codePattern.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	_, err := time.Parse("02012006", m[1])
	return err == nil
}

// NewCode makes a code for an order created at the given time, with four random characters.
func NewCode(created time.Time) string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // the system's random source failing is not something to carry on through
	}
	suffix := make([]byte, 4)
	for i, v := range b {
		suffix[i] = CodeAlphabet[v%32] // 256 is a multiple of 32: every character is equally likely
	}
	return "TRX-" + created.In(jakarta).Format("02012006") + "-" + string(suffix)
}
