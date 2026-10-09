package agent

// Target grounding (D60). A step that changes or deletes an EXISTING record names it by
// id. The model must never guess that id: «کارمند زهرا معتضد رو حذف کن» was planned as
// employees.delete {id: 1} without any lookup, and employee 1 was someone else. Before a
// plan is presented every such id is checked against the application:
//
//   - the record exists (read by id, or found in its list);
//   - the id came from the application in this work: a read of this plan, a completed
//     step, or a document of the conversation whose label is this record's name;
//   - when the user named a record and the reads show it, the step targets that record.
//
// The record's real name is shown on the card, so the user approves the record, not an id.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// targetKinds are the operation kinds that act on an existing record.
var targetKinds = map[string]bool{"update": true, "delete": true, "workflow": true}

// recordName is the human name of a record read from the application.
func recordName(rec map[string]any) string {
	for _, k := range []string{"full_name", "name", "title", "label", "description", "code"} {
		if s, ok := rec[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	if fn, ln := fmt.Sprint(rec["first_name"]), fmt.Sprint(rec["last_name"]); rec["last_name"] != nil {
		return strings.TrimSpace(strings.TrimPrefix(fn, "<nil>") + " " + ln)
	}
	if n, ok := rec["number"]; ok && n != nil {
		return "#" + fmt.Sprint(n)
	}
	return ""
}

// idSeen reports whether the application returned a record with this id in text.
func idSeen(text, id string) bool {
	if text == "" {
		return false
	}
	re := regexp.MustCompile(`"id"\s*:\s*"?` + regexp.QuoteMeta(id) + `"?\s*[,}]`)
	return re.MatchString(text)
}

// readObjects walks a read result for objects that carry an id and a name.
func readObjects(raw []byte) []map[string]any {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	var out []map[string]any
	var walk func(x any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			if _, ok := t["id"]; ok && recordName(t) != "" {
				out = append(out, t)
			}
			for _, y := range t {
				walk(y)
			}
		case []any:
			for _, y := range t {
				walk(y)
			}
		}
	}
	walk(v)
	return out
}

// groundTargets checks every record id the pending steps act on. It returns feedback for
// the planner ("" when every target is grounded) and records the target names on the steps.
func (a *Agent) groundTargets(ctx context.Context, u *User, st *State, c *conversation, steps []PlanStep) string {
	p := st.Plan
	said := normalize.Text(strings.Join(append([]string{p.Goal}, p.UserTexts...), " "))
	var seen []string // what the application returned in this work
	for _, r := range p.Reads {
		if strings.HasSuffix(r.Tool, currentTag) {
			continue // read only to complete an update's payload: proves nothing about the id
		}
		if len(r.Raw) > 0 {
			seen = append(seen, string(r.Raw))
		} else {
			seen = append(seen, r.Result)
		}
	}
	for _, s := range p.Steps {
		if s.Status == "done" {
			seen = append(seen, string(s.Result))
		}
	}
	for i := range steps {
		s := &steps[i]
		op, ok := catalog.Load().Get(s.Tool)
		if !ok || !targetKinds[op.Kind] || !containsStr(op.PathParams, "id") {
			continue
		}
		id := s.Args.Path["id"]
		if id == "" || refRe.MatchString(id) {
			continue // created by an earlier step of this plan: grounded by construction
		}
		if _, err := strconv.ParseInt(id, 10, 64); err != nil {
			return fmt.Sprintf("step %s (%s): id %q is not a record id — look the record up with its list tool and use the id it returns", s.ID, s.Tool, id)
		}
		rec, _ := a.readRecord(ctx, u, op, id, s.Args)
		if rec == nil {
			return fmt.Sprintf("step %s (%s): there is no record with id %s — never guess ids; look the record the user named up with its list tool (q=<name>) and use the id from the result", s.ID, s.Tool, id)
		}
		name := recordName(rec)
		grounded := false
		for _, txt := range seen {
			if idSeen(txt, id) {
				grounded = true
				break
			}
		}
		if !grounded && c != nil && name != "" {
			for _, art := range c.state.Artifacts {
				if strconv.FormatInt(art.ID, 10) == id && normalize.Text(art.Label) == normalize.Text(name) {
					grounded = true
				}
			}
		}
		if !grounded {
			return fmt.Sprintf("step %s (%s) uses id %s, which was never read in this conversation (that id is «%s»). Never guess ids: first read the record the user named with its list tool (q=<name>), then use the id from the result",
				s.ID, s.Tool, id, name)
		}
		// the user named a record and the reads show it under another id: wrong row
		if name != "" && !strings.Contains(said, normalize.Text(name)) {
			for _, txt := range seen {
				for _, o := range readObjects([]byte(txt)) {
					on := recordName(o)
					oid := strconv.FormatInt(int64Of(o["id"]), 10)
					if oid != id && len([]rune(on)) >= 3 && strings.Contains(said, normalize.Text(on)) {
						return fmt.Sprintf("step %s (%s): the user named «%s» (id %s) but the step uses id %s («%s») — use the record the user named", s.ID, s.Tool, on, oid, id, name)
					}
				}
			}
		}
		s.Target = &StepTarget{ID: id, Name: name}
	}
	return ""
}

// resolveNamedTargets: the model put the record's NAME where its id belongs
// (cost_centers.delete {id: "عیاشی"}, three rounds in a row). The name is looked up in
// the application: exactly one record with that name → its id (the read is kept in the
// plan, so the id is grounded); several → the planner must ask which one; none → say so.
// Returns feedback for the planner, or "".
func (a *Agent) resolveNamedTargets(ctx context.Context, u *User, st *State, calls []plannerCall) string {
	for i := range calls {
		cl := &calls[i]
		op, ok := catalog.Load().Get(cl.Tool)
		if !ok || !targetKinds[op.Kind] || !containsStr(op.PathParams, "id") {
			continue
		}
		if cl.Path == nil {
			continue
		}
		path := cl.Path
		name := strings.TrimSpace(path["id"])
		if name == "" || refRe.MatchString(name) {
			continue
		}
		if _, err := strconv.ParseInt(normalize.Digits(name), 10, 64); err == nil {
			if d := normalize.Digits(name); d != name {
				path["id"] = d // Persian digits
			}
			continue
		}
		lo := listOpFor(op)
		if lo == nil {
			return fmt.Sprintf("step %s (%s): %q is a name, not an id — read the record with a list tool first and use its id", cl.ID, cl.Tool, name)
		}
		q := tools.Scalars{}
		if containsStr(lo.QueryParams, "q") {
			q["q"] = name
		}
		if containsStr(lo.QueryParams, "page_size") {
			q["page_size"] = "200"
		}
		call := plannerCall{Tool: lo.ID, Query: q}
		rec := a.planRead(ctx, u, nil, call, nopEmitter{})
		var matches []map[string]any
		items := recordItems(json.RawMessage(rec.Raw))
		for _, o := range items {
			if normalize.Text(recordName(o)) == normalize.Text(name) {
				matches = append(matches, o)
			}
		}
		if len(matches) == 0 {
			// the model put part of the name in the id slot («خطابخظلر» for «آزمایش
			// خطابخظلر»): a record whose FULL name the user wrote and that contains it
			said := " " + normalize.Text(strings.Join(append([]string{st.Plan.Goal}, st.Plan.UserTexts...), " ")) + " "
			for _, o := range items {
				full := normalize.Text(recordName(o))
				if full != "" && strings.Contains(full, normalize.Text(name)) && strings.Contains(said, " "+full) {
					matches = append(matches, o)
				}
			}
		}
		switch len(matches) {
		case 1:
			path["id"] = strconv.FormatInt(int64Of(matches[0]["id"]), 10)
			st.Plan.Reads = append(st.Plan.Reads, rec)
		case 0:
			return fmt.Sprintf("step %s (%s): there is no record named «%s» (searched %s). Tell the user plainly; do not guess another record", cl.ID, cl.Tool, name, lo.ID)
		default:
			var opts []string
			for _, m := range matches {
				opts = append(opts, fmt.Sprintf("id %v (%s)", m["id"], firstNonEmpty(fmt.Sprint(m["code"]), "")))
			}
			st.Plan.Reads = append(st.Plan.Reads, rec)
			return fmt.Sprintf("step %s (%s): several records are named «%s»: %s — use the ask action so the user chooses one", cl.ID, cl.Tool, name, strings.Join(opts, ", "))
		}
	}
	return ""
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" && x != "<nil>" {
			return x
		}
	}
	return ""
}

// nopEmitter discards events (internal lookups the user does not need to see).
type nopEmitter struct{}

func (nopEmitter) Emit(string, any) {}

// verifyReference grounds a reference the planner did not read: the system reads the
// referenced record itself and accepts it only when it exists AND its name is in the
// user's words (identity, not mere existence). The read is kept in the plan. Returns ""
// when grounded, otherwise the reason.
func (a *Agent) verifyReference(ctx context.Context, u *User, st *State, ri refIssue) string {
	p := st.Plan
	said := " " + normalize.Text(strings.Join(append([]string{p.Goal}, p.UserTexts...), " ")) + " "
	for _, tool := range ri.Tools {
		if !strings.HasSuffix(tool, ".list") {
			continue
		}
		op, ok := catalog.Load().Get(tool)
		if !ok {
			continue
		}
		q := tools.Scalars{}
		if containsStr(op.QueryParams, "page_size") {
			q["page_size"] = "500"
		}
		path := tools.Scalars{}
		if containsStr(op.PathParams, "kind") {
			continue // invoices need their kind: the planner must read them itself
		}
		rec := a.planRead(ctx, u, nil, plannerCall{Tool: tool, Path: path, Query: q}, nopEmitter{})
		if !rec.OK {
			continue
		}
		items := recordItems(json.RawMessage(rec.Raw))
		active := 0
		for _, o := range items {
			if v, ok := o["is_active"].(bool); !ok || v {
				active++
			}
		}
		for _, o := range items {
			if strconv.FormatInt(int64Of(o["id"]), 10) != ri.Val {
				continue
			}
			if def, _ := o["is_default"].(bool); active == 1 || def {
				p.Reads = append(p.Reads, rec) // the only record of its kind, or the application's default: not a guess
				return ""
			}
			name := normalize.Text(recordName(o))
			if name != "" && (strings.Contains(said, " "+name) || (len([]rune(name)) >= 3 && strings.Contains(said, name))) {
				p.Reads = append(p.Reads, rec)
				return ""
			}
			return fmt.Sprintf("step %s: %s=%s is «%s», which the user did not name — read %s and use the record the user named", ri.Step, ri.Key, ri.Val, recordName(o), tool)
		}
		return fmt.Sprintf("step %s: there is no record %s=%s (%s) — never guess ids", ri.Step, ri.Key, ri.Val, tool)
	}
	return ri.String()
}

// completeUpdates: every update of this application is a full-replacement PUT — a field
// the body leaves out is wiped («نرگس احمدی رو … سرتختی بشه» renamed her AND set her base
// salary from 95,000,000 to 0). So the step's body is merged over the record's CURRENT
// values read from the application: every writable field the user did not ask to change
// keeps its value. The fields the model set are remembered (Intended) so verification can
// prove nothing else changed. The read is tagged: it never grounds the id (D60).
func (a *Agent) completeUpdates(ctx context.Context, u *User, st *State, calls []plannerCall) {
	for i := range calls {
		cl := &calls[i]
		op, ok := catalog.Load().Get(cl.Tool)
		if !ok || op.Kind != "update" || op.Method != "PUT" || cl.Path == nil {
			continue
		}
		id := cl.Path["id"]
		if _, err := strconv.ParseInt(id, 10, 64); err != nil {
			continue
		}
		var body map[string]json.RawMessage
		if len(cl.Body) > 0 && json.Unmarshal(cl.Body, &body) != nil {
			continue
		}
		if body == nil {
			body = map[string]json.RawMessage{}
		}
		rec, raw := a.readRecord(ctx, u, op, id, tools.OpArgs{Path: cl.Path})
		if rec == nil {
			continue
		}
		cl.intended = cl.intended[:0]
		for k := range body {
			cl.intended = append(cl.intended, k)
		}
		for _, f := range op.InputFields() {
			if _, set := body[f.Name]; set {
				continue
			}
			if v, ok := rec[f.Name]; ok {
				b, _ := json.Marshal(v)
				body[f.Name] = b
			}
		}
		cl.Body, _ = json.Marshal(body)
		if rb := readBackOp(op); rb != nil {
			// tagged: completing a payload never makes the id itself "grounded" (D60)
			st.Plan.Reads = append(st.Plan.Reads, ReadRecord{Tool: rb.ID + currentTag, Args: tools.OpArgs{Path: tools.Scalars{"id": id}}, OK: true, Raw: raw, Result: truncate(string(raw), 3500)})
		}
	}
}

// untouchedChanged lists the fields an update was NOT asked to change that read back
// differently from before (D63): a full-replacement write must not wipe them.
func untouchedChanged(op *catalog.Operation, intended []string, before, after json.RawMessage) []string {
	if len(intended) == 0 || len(before) == 0 || len(after) == 0 {
		return nil
	}
	var b, a map[string]any
	_ = json.Unmarshal(before, &b)
	_ = json.Unmarshal(after, &a)
	want := map[string]bool{}
	for _, k := range intended {
		want[k] = true
	}
	var out []string
	for _, f := range op.InputFields() {
		if want[f.Name] || volatileKey(f.Name) {
			continue
		}
		bv, ok1 := b[f.Name]
		av, ok2 := a[f.Name]
		if !ok1 || !ok2 {
			continue
		}
		bj, _ := json.Marshal(bv)
		aj, _ := json.Marshal(av)
		if string(bj) == string(aj) || sameValue(strings.Trim(string(bj), `"`), av) {
			continue
		}
		out = append(out, fmt.Sprintf("%s changed from %s to %s although it was not to be changed", f.Name, truncate(string(bj), 60), truncate(string(aj), 60)))
	}
	sort.Strings(out)
	return out
}

// currentTag marks a read made to complete an update's payload (not a lookup).
const currentTag = ":current"

// completeCheckBank: a check needs its bank, and the user named it («روی بانک ملت») but the
// model left it out. The bank is looked up in the application's bank list by the name the
// user wrote; exactly one match is used (the read grounds it), otherwise nothing is filled
// and the user is asked as before.
func (a *Agent) completeCheckBank(ctx context.Context, u *User, st *State, calls []plannerCall) {
	said := " " + normalize.Text(strings.Join(append([]string{st.Plan.Goal}, st.Plan.UserTexts...), " ")) + " "
	for i := range calls {
		cl := &calls[i]
		if cl.Tool != "checks.create" && cl.Tool != "checks.update" {
			continue
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(cl.Body, &body) != nil {
			continue
		}
		if t := strings.TrimSpace(string(body["bank_id"])); t != "" && t != "null" {
			continue
		}
		if t := strings.Trim(strings.TrimSpace(string(body["bank_name"])), `"`); t != "" && t != "null" {
			continue
		}
		rec := a.planRead(ctx, u, nil, plannerCall{Tool: "checks.banks.list"}, nopEmitter{})
		if !rec.OK {
			continue
		}
		var hits []map[string]any
		for _, b := range recordItems(json.RawMessage(rec.Raw)) {
			n := strings.TrimSpace(strings.TrimPrefix(normalize.Text(recordName(b)), normalize.Text("بانک")))
			if len([]rune(n)) >= 2 && strings.Contains(said, " "+normalize.Text("بانک")+" "+n+" ") {
				hits = append(hits, b)
			}
		}
		if len(hits) != 1 {
			continue
		}
		body["bank_id"], _ = json.Marshal(int64Of(hits[0]["id"]))
		cl.Body, _ = json.Marshal(body)
		st.Plan.Reads = append(st.Plan.Reads, rec)
	}
}
