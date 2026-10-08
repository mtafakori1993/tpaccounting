package classify

import (
	"encoding/json"
	"sort"
	"strings"
)

// SlotStatus tracks what we know about each field of the current operation.
type SlotStatus string

const (
	Known    SlotStatus = "known"    // the user (or a document) gave a value
	Null     SlotStatus = "null"     // the user explicitly said there is none ("no code") — a COMPLETE answer
	Inferred SlotStatus = "inferred" // derived by the assistant (shown in the preview, never asked again)
	Unknown  SlotStatus = "unknown"  // not provided yet
)

// Frame is the structured state of the operation being discussed across turns.
// New information is MERGED into it; it is never rebuilt from the latest message.
type Frame struct {
	Intent    string                `json:"intent"`
	Event     Event                 `json:"event"`
	Status    map[string]SlotStatus `json:"status"`
	Turns     int                   `json:"turns"`
	UserTexts []string              `json:"user_texts,omitempty"` // every user message of this operation, in order
}

// eventFields lists Event's JSON field names (merge works on these names).
func eventFields() map[string]bool {
	b, _ := json.Marshal(Event{})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

var fieldSet = eventFields()

// IsField reports whether name is a valid Event field.
func IsField(name string) bool { return fieldSet[name] }

func zero(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case float64:
		return x == 0
	case bool:
		return false // an explicit boolean is information
	case []any:
		return len(x) == 0
	}
	return false
}

// NewFrame starts a frame from a first interpretation.
func NewFrame(it *Interpretation, userText string) *Frame {
	f := &Frame{Intent: it.Intent, Status: map[string]SlotStatus{}}
	f.Merge(it.Event, it.ExplicitNull, userText)
	return f
}

// Merge applies an update: non-empty fields are set (known), fields the user explicitly
// declined become null, everything else keeps its previous value and status. The event
// type (the operation) is kept unless the caller changes it explicitly.
func (f *Frame) Merge(upd Event, explicitNull []string, userText string) {
	if f.Status == nil {
		f.Status = map[string]SlotStatus{}
	}
	cur := map[string]any{}
	b, _ := json.Marshal(f.Event)
	_ = json.Unmarshal(b, &cur)
	nu := map[string]any{}
	b, _ = json.Marshal(upd)
	_ = json.Unmarshal(b, &nu)
	keepType := cur["type"]
	for k, v := range nu {
		if k == "type" || zero(v) {
			continue
		}
		cur[k] = v
		f.Status[k] = Known
	}
	for _, k := range explicitNull {
		k = strings.TrimSpace(k)
		if !IsField(k) || k == "type" {
			continue
		}
		delete(cur, k)
		f.Status[k] = Null
	}
	if zero(keepType) {
		cur["type"] = nu["type"]
	} else {
		cur["type"] = keepType
	}
	b, _ = json.Marshal(cur)
	var merged Event
	_ = json.Unmarshal(b, &merged)
	f.Event = merged
	f.Turns++
	if t := strings.TrimSpace(userText); t != "" {
		f.UserTexts = append(f.UserTexts, t)
	}
}

// IsNull reports an explicit "none" answer for field.
func (f *Frame) IsNull(field string) bool { return f != nil && f.Status[field] == Null }

// Has reports whether a field is answered (known, null or inferred).
func (f *Frame) Has(field string) bool {
	if f == nil {
		return false
	}
	switch f.Status[field] {
	case Known, Null, Inferred:
		return true
	}
	return false
}

// Summary lists the frame for the model, so a follow-up is read as an UPDATE.
func (f *Frame) Summary() string {
	var keys []string
	for k := range f.Status {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b, _ := json.Marshal(f.Event)
	var vals map[string]any
	_ = json.Unmarshal(b, &vals)
	var sb strings.Builder
	sb.WriteString("operation: " + f.Event.Type + "\n")
	for _, k := range keys {
		switch f.Status[k] {
		case Null:
			sb.WriteString(k + ": NONE (user said there is none)\n")
		default:
			if v, ok := vals[k]; ok && !zero(v) {
				vb, _ := json.Marshal(v)
				sb.WriteString(k + ": " + string(vb) + " (" + string(f.Status[k]) + ")\n")
			}
		}
	}
	return sb.String()
}

// UserText is the whole request so far (all user turns of this operation).
func (f *Frame) UserText() string { return strings.Join(f.UserTexts, "\n") }
