// Package execmode decides HOW the assistant carries out an operation: by operating
// the application's real screen like an employee (HumanUI, the default) or through the
// application's services (Direct). It is the single place this is decided.
//
//	HumanUI  real UI, visible pointer, real clicks and typing, real submission
//	Direct   application service/API, no pointer
//
// It is deliberately independent of confirmation, approval, permission and safety
// (package policy) and of the "show me on screen, don't submit" preview (policy
// ActionUIPreview): those answer *whether* and *whether to ask*; this answers *how*.
// Nothing here ever derives Direct from the mere existence of an API.
package execmode

import (
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
)

type Mode string

const (
	HumanUI Mode = "human_ui"
	Direct  Mode = "direct"
)

// Default is the application default: operate the software like a human employee.
const Default = HumanUI

// Source says which rule decided the mode (audited, shown to the user).
type Source string

const (
	SourceRequest    Source = "request"    // the user's words in this very message
	SourceExplicit   Source = "explicit"   // an explicit choice already attached to the operation (button, API field, earlier turn)
	SourcePreference Source = "preference" // the user's persistent setting
	SourceDefault    Source = "default"
)

// Parse reads a stored/wire value. The legacy "ui"/"api" spellings are accepted.
func Parse(s string) (Mode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "human_ui", "human", "ui":
		return HumanUI, true
	case "direct", "api":
		return Direct, true
	}
	return "", false
}

// Valid reports whether s is a persistable preference.
func Valid(s string) bool {
	m, ok := Parse(s)
	return ok && (s == string(m))
}

var directPhrases = []string{
	"مستقیم", "بدون موس", "بدون ماوس", "بدون رابط", "بدون کلیک", "پشت صحنه", "از طریق api", "با api", "از طریق سرویس", "با سرویس",
	"directly", "direct", "via api", "through the api", "using the api", "via the api", "without the mouse", "without the ui", "no ui",
	"behind the scenes", "in the background",
}

var humanPhrases = []string{
	"با موس", "با ماوس", "با کلیک", "مثل انسان", "مثل آدم", "مثل کارمند", "مثل یک کارمند", "مثل یک انسان", "روی صفحه انجام", "روی صفحه بزن",
	"از طریق رابط", "از طریق صفحه", "با رابط کاربری", "با ui", "خودت روی صفحه",
	"with the mouse", "with mouse", "using the mouse", "with the pointer", "like a human", "like an employee", "like a person",
	"on the screen yourself", "on screen yourself", "using the ui", "through the ui", "with the ui", "by clicking", "do it on screen", "do it on the screen",
}

// Detect finds an explicit mode in the user's own words. It uses only the user's message
// (never document text). When both appear, seeing it on screen wins: that is the safe
// reading and the application default.
func Detect(userText string) (Mode, bool) {
	t := " " + normalize.Text(userText) + " "
	has := func(list []string) bool {
		for _, p := range list {
			n := normalize.Text(p)
			if n == "" {
				continue
			}
			// Latin words must match whole words ("direct" ≠ "director"); Persian prefixes
			// may carry suffixes ("مستقیماً", "مستقیمش").
			if isLatin(n) {
				if strings.Contains(t, " "+n+" ") {
					return true
				}
			} else if strings.Contains(t, " "+n) {
				return true
			}
		}
		return false
	}
	switch {
	case has(humanPhrases):
		return HumanUI, true
	case has(directPhrases):
		return Direct, true
	}
	return "", false
}

func isLatin(s string) bool {
	for _, r := range s {
		if r >= 0x80 {
			return false
		}
	}
	return true
}

// Input are the facts the resolver works from.
type Input struct {
	Request    string // the user's message
	Explicit   Mode   // a choice already attached to this operation (pending-action instruction), or ""
	Preference string // the user's persistent preference ("human_ui" | "direct" | "")
}

type Resolution struct {
	Mode   Mode   `json:"mode"`
	Source Source `json:"source"`
}

// Resolve applies the priority order:
//  1. explicit instruction in the current request
//  2. explicit instruction already attached to the pending action
//  3. the user's persistent preference
//  4. the application default (HumanUI)
func Resolve(in Input) Resolution {
	if m, ok := Detect(in.Request); ok {
		return Resolution{m, SourceRequest}
	}
	if in.Explicit == HumanUI || in.Explicit == Direct {
		return Resolution{in.Explicit, SourceExplicit}
	}
	if m, ok := Parse(in.Preference); ok {
		return Resolution{m, SourcePreference}
	}
	return Resolution{Default, SourceDefault}
}

// fillers are the words around a bare mode instruction («به‌جای آن مستقیم اجرا کن»,
// «لطفا همین رو با موس انجام بده», "just do it directly").
var fillers = map[string]bool{
	"به": true, "جای": true, "بجای": true, "به‌جای": true, "آن": true, "ان": true, "این": true, "اینو": true, "اونو": true, "همین": true, "همینو": true,
	"رو": true, "را": true, "لطفا": true, "لطفاً": true, "خواهشا": true, "پس": true, "حالا": true, "دیگه": true, "دیگر": true, "فقط": true, "خودت": true, "کار": true,
	"اجرا": true, "اجراش": true, "انجام": true, "انجامش": true, "ثبت": true, "ثبتش": true, "کن": true, "بکن": true, "بده": true, "بزن": true, "بشه": true, "شود": true,
	"از": true, "طریق": true, "برنامه": true, "روی": true, "صفحه": true, "و": true, "بی": true, "بدون": true, "با": true, "مثل": true, "یک": true, "یه": true,
	"just": true, "please": true, "do": true, "it": true, "run": true, "instead": true, "then": true, "now": true, "the": true, "this": true, "that": true,
	"go": true, "ahead": true, "execute": true, "register": true, "on": true, "screen": true, "yourself": true, "via": true, "through": true, "using": true,
}

// OnlyInstruction reports whether the message is nothing but an execution-mode
// instruction for the operation already open («مستقیم اجرا کن», "run it directly
// instead"). Such a message changes HOW the same operation runs; it is never a new
// request and never re-plans (D59).
func OnlyInstruction(userText string) (Mode, bool) {
	m, ok := Detect(userText)
	if !ok {
		return "", false
	}
	t := " " + normalize.Text(userText) + " "
	for _, list := range [][]string{directPhrases, humanPhrases} {
		for _, p := range list {
			if n := normalize.Text(p); n != "" {
				t = strings.ReplaceAll(t, " "+n+" ", " ")
			}
		}
	}
	for _, w := range strings.FieldsFunc(t, func(r rune) bool { return r == ' ' || r == '‌' || r == '.' || r == '!' || r == '،' || r == ',' }) {
		if fillers[w] || fillers[strings.TrimSuffix(w, "ً")] || strings.HasPrefix(w, "مستقیم") || w == "" {
			continue
		}
		return "", false
	}
	return m, true
}
