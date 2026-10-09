package execmode

import "testing"

func TestDetect(t *testing.T) {
	cases := []struct {
		text string
		want Mode
		ok   bool
	}{
		{"یه مشتری به اسم شرکت قاسم ایجاد کن", "", false},
		{"یه مشتری به اسم شرکت قاسم ایجاد کن، مستقیم انجام بده", Direct, true},
		{"مستقیم ثبتش کن", Direct, true},
		{"این مشتری را مستقیم ایجاد کن", Direct, true},
		{"مستقیماً ثبت کن", Direct, true},
		{"با موس انجامش بده", HumanUI, true},
		{"روی صفحه انجامش بده", HumanUI, true},
		{"create the customer directly", Direct, true},
		{"create the customer with the mouse", HumanUI, true},
		{"ask the director to approve", "", false}, // whole words only
		{"با موس و مستقیم", HumanUI, true},         // both: seeing it wins
		{"PDF این رو بده", "", false},
		{"مستقیم PDF تولید کن", Direct, true},
		{"نشانم بده", "", false}, // preview is not a mode
	}
	for _, c := range cases {
		m, ok := Detect(c.text)
		if m != c.want || ok != c.ok {
			t.Errorf("%q: got (%q,%v) want (%q,%v)", c.text, m, ok, c.want, c.ok)
		}
	}
}

func TestResolvePriority(t *testing.T) {
	// default is the human UI — never Direct just because an API exists
	if r := Resolve(Input{Request: "یه مشتری ایجاد کن"}); r.Mode != HumanUI || r.Source != SourceDefault {
		t.Fatalf("default: %+v", r)
	}
	// preference beats default
	if r := Resolve(Input{Request: "x", Preference: "direct"}); r.Mode != Direct || r.Source != SourcePreference {
		t.Fatalf("preference: %+v", r)
	}
	// pending explicit beats preference
	if r := Resolve(Input{Request: "x", Explicit: HumanUI, Preference: "direct"}); r.Mode != HumanUI || r.Source != SourceExplicit {
		t.Fatalf("explicit: %+v", r)
	}
	// the current request beats everything
	if r := Resolve(Input{Request: "با موس انجامش بده", Explicit: Direct, Preference: "direct"}); r.Mode != HumanUI || r.Source != SourceRequest {
		t.Fatalf("request: %+v", r)
	}
	if r := Resolve(Input{Request: "directly please", Preference: "human_ui"}); r.Mode != Direct || r.Source != SourceRequest {
		t.Fatalf("request direct: %+v", r)
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]Mode{"ui": HumanUI, "HUMAN_UI": HumanUI, "api": Direct, "direct": Direct} {
		if m, ok := Parse(in); !ok || m != want {
			t.Errorf("%s: %q %v", in, m, ok)
		}
	}
	if _, ok := Parse("preview"); ok {
		t.Error("preview must not be a mode")
	}
	if Valid("api") || !Valid("direct") || !Valid("human_ui") {
		t.Error("only canonical values are persistable")
	}
}

func TestOnlyInstruction(t *testing.T) {
	for _, s := range []string{"مستقیم اجرا کن", "به‌جای آن مستقیم اجرا کن", "مستقیم انجام بده", "مستقیماً ثبتش کن", "لطفا با موس انجام بده", "run it directly instead", "do it directly"} {
		if _, ok := OnlyInstruction(s); !ok {
			t.Errorf("%q should be a bare mode instruction", s)
		}
	}
	for _, s := range []string{"مستقیم ۵ میلیون به علی پرداخت کن", "کارمند زهرا معتضد رو مستقیم حذف کن", "یه رسید خرید بزن", "directly pay Ali 5 million"} {
		if _, ok := OnlyInstruction(s); ok {
			t.Errorf("%q carries a request, not only a mode", s)
		}
	}
}
