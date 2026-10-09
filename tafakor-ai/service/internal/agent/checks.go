package agent

// Check registration (D67). The application's own rules decide, not the model:
//
//	check_type   only "received" | "issued" (checks/service.go). «صیادی» is the national
//	             registry (sayadi_id), never a type; «در وجه X» / «صادره» / «از حساب …» →
//	             issued, «دریافتی» / «وارده» → received
//	posting      there is no checks.post operation: `checks.post` is a PERMISSION. An issued
//	             check is created as a draft (the form's default) or issued at once with
//	             `issue: true` on checks.create (the form's «صدور فوری»); later issuing is
//	             checks.transitions {action: issue}
//	dates        issue_date «تاریخ صدور» and due_date «تاریخ سررسید» are both required.
//	             «سررسید X» → due, «صدور X» → issue. A bare «تاریخ X» is the date written on
//	             the check — its due date — when X is after today; otherwise the user is
//	             asked which date it is. A missing issue/received date is today (the form's
//	             default) and is shown on the card. Dates are converted by code
//	             (normalize.DatesIn), never by the model.

import (
	"encoding/json"
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
)

// checkDates reads the roles of the dates the user gave for a check.
func checkDates(p *PlanState, today string) (issue, due string, bare []normalize.TextDate) {
	for _, txt := range append([]string{p.Goal}, p.UserTexts...) {
		n := normalize.Digits(txt)
		for _, d := range normalize.DatesIn(txt) {
			if !d.Valid {
				continue
			}
			before := n[:d.Pos]
			if i := strings.LastIndexAny(before, "،,.؛\n"); i >= 0 {
				before = before[i:]
			}
			w := normalize.Text(lastWords(before, 3))
			switch {
			case strings.Contains(w, "سررسید") || strings.Contains(w, "due"):
				due = d.Gregorian
			case strings.Contains(w, "صدور") || strings.Contains(w, "issue"):
				issue = d.Gregorian
			default:
				bare = append(bare, d)
			}
		}
	}
	return issue, due, bare
}

func lastWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[len(f)-n:]
	}
	return strings.Join(f, " ")
}

// issueNowSaid: the user asked to issue (post) the check now, not only to register it.
func issueNowSaid(p *PlanState) bool {
	t := " " + normalize.Text(strings.Join(append([]string{p.Goal}, p.UserTexts...), " ")) + " "
	for _, w := range []string{"صادر کن", "صادرش کن", "صدور فوری", "صدور کن", "صادر شود", "تحویل دادیم", "تحویل شد", "issue it", "issue now"} {
		if strings.Contains(t, " "+normalize.Text(w)) {
			return true
		}
	}
	return false
}

// completeChecks fills a checks.create step from the user's words and the application's
// defaults. It returns a question when a date's meaning cannot be established, and notes
// for the card.
func completeChecks(p *PlanState, calls []plannerCall, today string) (question string, notes []string) {
	for i := range calls {
		cl := &calls[i]
		if cl.Tool != "checks.create" {
			continue
		}
		body := map[string]json.RawMessage{}
		if len(cl.Body) > 0 && json.Unmarshal(cl.Body, &body) != nil {
			continue
		}
		set := func(k string, v any) { body[k], _ = json.Marshal(v) }
		str := func(k string) string {
			var s string
			_ = json.Unmarshal(body[k], &s)
			return s
		}
		typ := checkTypeSaid(p)
		if typ != "" {
			set("check_type", typ)
		} else if v := str("check_type"); v != "received" && v != "issued" {
			delete(body, "check_type") // never a model guess like «صیادی»: the guard asks
		}
		issue, due, bare := checkDates(p, today)
		for _, d := range bare {
			switch {
			case due == "" && d.Gregorian > today:
				due = d.Gregorian // the date written on the check: when it is payable
				notes = append(notes, "«"+d.Raw+"» = تاریخ سررسید (تاریخ روی چک)")
			case due == "" && issue == "":
				return "تاریخ «" + d.Raw + "» تاریخ سررسید چک است یا تاریخ صدور آن؟ (اگر صدور است، سررسید را هم بگویید)", nil
			}
		}
		if due != "" {
			set("due_date", due)
		}
		if issue == "" {
			issue = today
			notes = append(notes, "تاریخ صدور: امروز (پیش‌فرض فرم نرم‌افزار)")
		}
		set("issue_date", issue)
		if typ == "received" || str("check_type") == "received" {
			if _, ok := body["received_date"]; !ok || str("received_date") == "" {
				set("received_date", today)
			}
			delete(body, "issue")
		} else {
			set("issue", issueNowSaid(p)) // the form's default is a draft; «صدور فوری» only when asked
		}
		cl.Body, _ = json.Marshal(body)
	}
	return "", notes
}

// dropPostingAliases removes a step the model invented to "post" a record it creates in
// the same plan when the application has no such operation (checks.post: issuing is a
// field of checks.create). Returns the notes for the card.
func dropPostingAliases(calls []plannerCall) ([]plannerCall, []string) {
	creates := map[string]string{}
	for _, c := range calls {
		if strings.HasSuffix(c.Tool, ".create") {
			creates[c.ID] = strings.TrimSuffix(c.Tool, ".create")
		}
	}
	var out []plannerCall
	var notes []string
	for _, c := range calls {
		if c.Tool == "checks.post" || c.Tool == "checks.issue" {
			ref := c.Path["id"]
			if m := refRe.FindStringSubmatch(ref); m != nil && creates[m[1]] == "checks" {
				notes = append(notes, c.Tool+" is not an operation of the application: issuing is part of checks.create (issue)")
				continue
			}
		}
		out = append(out, c)
	}
	return out, notes
}
