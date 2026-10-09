package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/textlang"
)

// Record text in the interface language (D45). The planner writes descriptions, item
// lines, the plan summary and step purposes itself; with a Persian interface none of
// that may come out in English ("payment", "purchase of medicine"). The first time a
// plan does, the planner is told exactly which words to rewrite; if it does again, the
// known words are translated deterministically. Words the user or the document wrote
// (a Latin brand, a Persian name) are kept.

// languageSources: everything the user or the application actually wrote.
func languageSources(st *State, p *PlanState) []string {
	src := []string{st.UserText}
	if p != nil {
		src = append(src, p.UserTexts...)
		src = append(src, p.Goal)
		for _, r := range p.Reads {
			src = append(src, r.Result)
		}
		if b, err := json.Marshal(p.Entities); err == nil {
			src = append(src, string(b))
		}
	}
	for _, x := range st.Extractions {
		src = append(src, x.Summary())
	}
	return src
}

// planText visits every user-facing free-text value of the steps (purpose, and body
// fields such as description/notes at any depth). fix returns the replacement.
func planText(steps []PlanStep, summary *string, fix func(where, text string) string) {
	if summary != nil && *summary != "" {
		*summary = fix("summary", *summary)
	}
	for i := range steps {
		s := &steps[i]
		if s.Purpose != "" {
			s.Purpose = fix(s.ID+".purpose", s.Purpose)
		}
		if len(s.Args.Body) == 0 {
			continue
		}
		var body any
		if json.Unmarshal(s.Args.Body, &body) != nil {
			continue
		}
		changed := false
		var walk func(path string, v any) any
		walk = func(path string, v any) any {
			switch x := v.(type) {
			case map[string]any:
				keys := make([]string, 0, len(x))
				for k := range x {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					if str, ok := x[k].(string); ok && textlang.FreeTextKeys[k] && str != "" && !refRe.MatchString(str) {
						if n := fix(path+"."+k, str); n != str {
							x[k], changed = n, true
						}
						continue
					}
					x[k] = walk(path+"."+k, x[k])
				}
			case []any:
				for j := range x {
					x[j] = walk(fmt.Sprintf("%s.%d", path, j), x[j])
				}
			}
			return v
		}
		body = walk(s.ID, body)
		if changed {
			s.Args.Body, _ = json.Marshal(body)
		}
	}
}

// checkPlanLanguage returns feedback for the planner when the plan's text is not in the
// interface language. With translate set it rewrites the known words in place instead.
func checkPlanLanguage(st *State, p *PlanState, steps []PlanStep, summary *string, translate bool) string {
	src := languageSources(st, p)
	var bad []string
	planText(steps, summary, func(where, text string) string {
		if translate {
			out, _ := textlang.Localize(text, st.Lang, src...)
			return out
		}
		if w := textlang.Foreign(text, st.Lang, src...); len(w) > 0 {
			bad = append(bad, fmt.Sprintf("%s %q (%s)", where, text, strings.Join(w, ", ")))
		}
		return text
	})
	if len(bad) == 0 {
		return ""
	}
	want := "Persian (فارسی)"
	if st.Lang == "en" {
		want = "English"
	}
	return "LANGUAGE CHECK FAILED: the user's interface language is " + want + ". Every description, note, item line, summary and purpose " +
		"you write must be in " + want + " — keep only names, codes and words the user or the document wrote as they are. Rewrite: " + strings.Join(bad, "; ")
}
