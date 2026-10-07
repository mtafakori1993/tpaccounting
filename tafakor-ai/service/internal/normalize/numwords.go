package normalize

import (
	"strings"

	"github.com/shopspring/decimal"
)

// Spoken/written Persian number words → digits (for speech transcripts and chat).

var smallWords = map[string]int64{
	"صفر": 0, "یک": 1, "یه": 1, "دو": 2, "سه": 3, "چهار": 4, "چار": 4, "پنج": 5, "شش": 6, "شیش": 6, "هفت": 7, "هشت": 8, "نه": 9,
	"ده": 10, "یازده": 11, "دوازده": 12, "سیزده": 13, "چهارده": 14, "پانزده": 15, "پونزده": 15, "شانزده": 16, "شونزده": 16,
	"هفده": 17, "هیفده": 17, "هجده": 18, "هیجده": 18, "نوزده": 19, "بیست": 20, "سی": 30, "چهل": 40, "پنجاه": 50, "شصت": 60,
	"هفتاد": 70, "هشتاد": 80, "نود": 90, "صد": 100, "یکصد": 100, "دویست": 200, "سیصد": 300, "چهارصد": 400, "پانصد": 500,
	"پونصد": 500, "ششصد": 600, "شیشصد": 600, "هفتصد": 700, "هشتصد": 800, "نهصد": 900,
}

var scaleWords = map[string]int64{
	"هزار": 1_000, "میلیون": 1_000_000, "ملیون": 1_000_000, "میلیارد": 1_000_000_000, "ملیارد": 1_000_000_000,
	"تریلیون": 1_000_000_000_000,
}

// Words that are numbers but also common words; never converted when alone.
var ambiguousAlone = map[string]bool{"نه": true, "یه": true, "یک": true, "سی": true, "ده": true, "صد": true}

var unitWords = map[string]bool{"تومان": true, "تومن": true, "ریال": true, "دلار": true, "یورو": true, "درهم": true}

type numTok struct {
	raw   string
	norm  string
	small *decimal.Decimal
	scale int64
	isAnd bool
	half  bool
}

func classify(raw string) numTok {
	t := numTok{raw: raw, norm: Text(raw)}
	n := t.norm
	// "بیست‌و‌پنج" written with ZWNJ collapses to "بیست و پنج" in Text; handled by splitting before.
	if n == "و" {
		t.isAnd = true
		return t
	}
	if n == "نیم" {
		t.half = true
		return t
	}
	if v, ok := smallWords[n]; ok {
		d := decimal.NewFromInt(v)
		t.small = &d
		return t
	}
	if v, ok := scaleWords[n]; ok {
		t.scale = v
		return t
	}
	if d, err := decimal.NewFromString(strings.ReplaceAll(Digits(raw), ",", "")); err == nil {
		t.small = &d
		return t
	}
	return t
}

func (t numTok) isNum() bool { return t.small != nil || t.scale > 0 || t.half }

// WordsToNumber parses a whole phrase like "دو میلیون و پانصد هزار" or "2.5 میلیون".
func WordsToNumber(s string) (decimal.Decimal, bool) {
	toks := tokens(s)
	if len(toks) == 0 {
		return decimal.Zero, false
	}
	v, n := parseSeq(toks)
	return v, n == len(toks) && n > 0
}

func tokens(s string) []numTok {
	s = strings.NewReplacer("‌", " ", "‌", " ").Replace(s)
	var out []numTok
	for _, f := range strings.Fields(s) {
		out = append(out, classify(f))
	}
	return out
}

// parseSeq consumes the longest numeric sequence at the start of toks.
func parseSeq(toks []numTok) (decimal.Decimal, int) {
	total, cur := decimal.Zero, decimal.Zero
	used, last := 0, -1
	sawNum := false
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch {
		case t.isAnd:
			// "و" must sit between two number tokens.
			if !sawNum || i+1 >= len(toks) || !toks[i+1].isNum() {
				goto end
			}
			continue
		case t.half:
			cur = cur.Add(decimal.NewFromFloat(0.5))
		case t.small != nil:
			// a bare digit token after other numbers ends the sequence ("5 10")
			if sawNum && last >= 0 && toks[last].small != nil && !isWord(toks[last]) && !isWord(t) {
				goto end
			}
			cur = cur.Add(*t.small)
		case t.scale > 0:
			if cur.IsZero() {
				cur = decimal.NewFromInt(1)
			}
			sc := decimal.NewFromInt(t.scale)
			if !total.IsZero() && total.LessThan(sc) {
				total = total.Add(cur).Mul(sc) // "صد هزار میلیون" style compounding
			} else {
				total = total.Add(cur.Mul(sc))
			}
			cur = decimal.Zero
		default:
			goto end
		}
		sawNum = true
		last = i
		used = i + 1
	}
end:
	return total.Add(cur), used
}

func isWord(t numTok) bool {
	_, ok := smallWords[t.norm]
	return ok
}

// ReplaceNumberWords rewrites every number-word sequence inside free text with
// ASCII digits, e.g. "دو میلیون و پانصد هزار تومان" → "2500000 تومان".
// Lone ambiguous words ("نه" = no/nine, "یه" = a) are left alone unless followed by a currency.
func ReplaceNumberWords(s string) string {
	s = Digits(s)
	toks := tokens(s)
	var out []string
	for i := 0; i < len(toks); {
		if !toks[i].isNum() || toks[i].isAnd {
			out = append(out, toks[i].raw)
			i++
			continue
		}
		v, n := parseSeq(toks[i:])
		if n == 0 {
			out = append(out, toks[i].raw)
			i++
			continue
		}
		if n == 1 && ambiguousAlone[toks[i].norm] {
			next := ""
			if i+1 < len(toks) {
				next = toks[i+1].norm
			}
			if !unitWords[next] {
				out = append(out, toks[i].raw)
				i++
				continue
			}
		}
		out = append(out, v.String())
		i += n
	}
	return strings.Join(out, " ")
}
