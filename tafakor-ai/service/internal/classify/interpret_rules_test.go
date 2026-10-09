package classify

import (
	"strings"
	"testing"
)

// "۱۰ میلیون به حساب بانک رفاه اضافه کن" must not be guessed as a transfer (D40):
// the source decides between opening balance, capital, receipt and transfer.
func TestAddingMoneyWithoutASourceIsAsked(t *testing.T) {
	for _, want := range []string{"AMBIGUOUS", "opening balance", "capital contribution", "transfer from another", "never treat it as editing a displayed balance",
		"Use transfer ONLY when the user names"} {
		if !strings.Contains(interpretSystem, want) {
			t.Errorf("interpretation rules lack %q", want)
		}
	}
}
