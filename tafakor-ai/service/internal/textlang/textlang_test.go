package textlang

import (
	"strings"
	"testing"
)

func TestForeignKeepsTheUsersOwnWords(t *testing.T) {
	if got := Foreign("پرداخت به Iranicard", "fa", "پرداخت به Iranicard را ثبت کن"); len(got) != 0 {
		t.Fatalf("a name the user typed is not foreign: %v", got)
	}
	if got := Foreign("payment for medicine", "fa", "خرید دارو"); strings.Join(got, ",") != "payment,for,medicine" {
		t.Fatalf("%v", got)
	}
	if got := Foreign("Payment to Ali", "en", "pay Ali"); len(got) != 0 {
		t.Fatalf("english interface: %v", got)
	}
	if got := Foreign("پرداخت دارو", "en", "buy medicine"); len(got) != 2 {
		t.Fatalf("persian invented in an english interface: %v", got)
	}
	if got := Foreign("مالیات VAT فاکتور INV-12", "fa", "INV-12"); len(got) != 0 {
		t.Fatalf("accepted abbreviations: %v", got)
	}
}

func TestLocalizeTranslatesCommonWords(t *testing.T) {
	out, left := Localize("Purchase of medicine", "fa", "خرید دارو از داروخانه")
	if out != "خرید دارو" || len(left) != 0 {
		t.Fatalf("%q %v", out, left)
	}
	out, left = Localize("payment to Iranicard", "fa", "")
	if out != "پرداخت به Iranicard" || len(left) != 1 {
		t.Fatalf("unknown words stay and are reported: %q %v", out, left)
	}
}
