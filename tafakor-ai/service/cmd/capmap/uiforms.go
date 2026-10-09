package main

// UI form discovery (D41): for every create/update operation whose endpoint the React
// app calls, find the real form a person fills in — the route it lives on, the control
// that opens it (a button, a variant button, the record's table row or an edit button on
// the record's page), the dialog it is rendered in and the control behind each request
// field — from the frontend SOURCE. The widget then opens and pre-fills that exact form
// for "show me on screen".
//
// The frontend's tags are language-independent: a field is tagged with the i18n key of
// its label (Field/Checkbox → data-ai-field), a button with the key of its text
// (data-ai-action) and a dialog with the key of its title (data-ai-dialog). So the key
// in the source IS the tag in the DOM.
//
// Unit of analysis: a function component. Its fields and dialog are its own; it is
// linked to its openers through the state variable that renders it:
//
//	<Button onClick={() => setEditing('new')}>{t('parties.newParty')}</Button>   create
//	<Button onClick={() => setEditing({ kind: 'bank' })}>…                       create variant kind=bank
//	onRowClick={(c) => setEditing(c)}                                            edit: click the record's row
//	{editing && <PartyDialog … />}                                               the form

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type UIField struct {
	Tag     string   `json:"tag"`
	Alts    []string `json:"alts,omitempty"` // the label is a ternary (customer/supplier, payer/payee): the tag depends on the variant
	Tab     string   `json:"tab,omitempty"`  // the control is shown only on this tab (data-ai-tab) of the form
	Control string   `json:"control"`        // fill | select | combo | date | check
}

// UIOpen is a control that opens the form; Field/Value name the variant it opens
// (kind=bank → "New bank account").
type UIOpen struct {
	Tag   string `json:"tag"`
	Field string `json:"field,omitempty"`
	Value string `json:"value,omitempty"`
}

type UIForm struct {
	Route   string             `json:"route"`             // route template: ":id", ":side", ":kind" are filled from the call
	Open    []UIOpen           `json:"open,omitempty"`    // controls that open the form
	Row     bool               `json:"row,omitempty"`     // edit: opened by clicking the record's table row
	Dialogs []string           `json:"dialogs,omitempty"` // dialog tags the form is rendered in ("*" = the open dialog); empty = page form
	Fields  map[string]UIField `json:"fields"`
	Lines   map[string]UIField `json:"lines,omitempty"`   // line-item fields (items[], lines[]): not pre-filled
	Missing []string           `json:"missing,omitempty"` // writable request fields the form has no control for
	File    string             `json:"file"`
}

type formField struct {
	key, field, control string
	alts                []string
	line                bool
	editOnly            bool   // rendered only for an existing record ({product && <Checkbox …/>})
	tab                 string // shown only on this tab ({mode === 'party' && …} with <Tabs value={mode}>)
}

type component struct {
	name, file string
	fields     []formField
	dialogKeys []string
	hasDialog  bool
	children   []string // components it renders
	hooks      []string // hooks it uses (useCheckForm): their requests are its requests
	calls      []string // API requests it sends: "METHOD /path" ("{}" for values)
}

type opener struct {
	file, key string // key "" = a table row
	arg       string // the setter's argument
	discr     [2]string
}

type tsxFile struct {
	path       string
	components []*component
	renders    map[string]string   // state variable → component rendered under it
	openers    map[string][]opener // state variable → controls that set it
	imports    []string
}

var (
	tKeyRe    = regexp.MustCompile(`\bt\(\s*'([A-Za-z0-9_.]+)'`)
	tagAttrRe = regexp.MustCompile(`\btag="([A-Za-z0-9_.]+)"`)
	compRe    = regexp.MustCompile(`(?m)^(?:export\s+)?(?:default\s+)?function\s+([A-Z]\w*|use[A-Z]\w*)\s*[(<]`)
	condRe    = regexp.MustCompile(`\{\s*(\w+)\s*&&\s*\(?\s*$`)
	propsRe   = regexp.MustCompile(`^[^(]*\(\s*\{([^}]*)\}`)
	hookUseRe = regexp.MustCompile(`\b(use[A-Z]\w*)\(`)
	setterRes = []*regexp.Regexp{
		regexp.MustCompile(`(?:^|[^.\w])(?:set|digits|ft|h)\(\s*'([a-z][a-z0-9_]*)'`),             // set('name', …), digits('mobile')
		regexp.MustCompile(`(?:^|[^.\w])set[A-Z]\w*\(\s*\{\s*\.\.\.\w+,\s*([a-z][a-z0-9_]*)\s*:`), // setV({ ...v, amount: x })
		regexp.MustCompile(`\{\s*\.\.\.\s*\w+\(\s*'([a-z][a-z0-9_]*)'\s*\)\s*\}`),                 // <Input {...field('legal_name')} />
		regexp.MustCompile(`onChange=\{\s*[a-z]\w*\(\s*'([a-z][a-z0-9_]*)'\s*\)\s*\}`),            // onChange={dg('account_number')}
	}
	lineSetterRe = regexp.MustCompile(`(?:^|[^.\w])update\(\s*[\w.]+,\s*\{\s*([a-z][a-z0-9_]*)\s*:`) // update(line, { quantity: v })
	controlRe    = regexp.MustCompile(`<(Select|[A-Z]\w*Select|[A-Z]\w*Picker|ComboBox|DateField|Checkbox|Switch|Input|MoneyInput|NumericInput|PercentageInput|AmountInput|TextArea)\b`)
	importRe     = regexp.MustCompile(`import\s+(?:type\s+)?\{[^}]*\}\s+from\s+'([@.][^']+)'`)
	lazyRe       = regexp.MustCompile(`const\s+(\w+)\s*=\s*page\(\(\)\s*=>\s*import\('([^']+)'\),\s*'(\w+)'\)`)
	directImpRe  = regexp.MustCompile(`import\s*\{\s*([\w\s,]+)\}\s*from\s*'(@/features/[^']+)'`)
	routeRe      = regexp.MustCompile(`<Route\s+(?:path="([^"]*)"|index)\s+element=\{<(\w+)`)
	renderRe     = regexp.MustCompile(`\{\s*!?\s*(\w+)\s*(?:&&|\?|===\s*[^&?]{1,20}&&|!==\s*[^&?]{1,20}&&)\s*\(?\s*<([A-Z]\w*)`)
	setCallRe    = regexp.MustCompile(`\bset([A-Z]\w*)\(\s*([^)]*?)\s*\)`)
	setRefRe     = regexp.MustCompile(`onRowClick=\{\s*(?:can\([^)]*\)\s*\?\s*)?set([A-Z]\w*)\s*(?:[:}])`)
	rowClickRe   = regexp.MustCompile(`onRowClick=\{`)
	objLitRe     = regexp.MustCompile(`^\{\s*(\w+)\s*:\s*'([\w-]+)'\s*\}$`)
	childRe      = regexp.MustCompile(`<([A-Z]\w*)[\s/>]`)
	apiPathRe    = regexp.MustCompile("api\\.(get|post|put|del)(?:<[^>()]*>)?\\(\\s*[`']([^`']+)[`']")
)

func controlOf(name string) string {
	switch {
	case name == "Checkbox" || name == "Switch":
		return "check"
	case name == "DateField":
		return "date"
	case strings.HasSuffix(name, "Picker") || name == "ComboBox":
		return "combo"
	case strings.HasSuffix(name, "Select"):
		return "select"
	}
	return "fill"
}

func isIdent(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// head is the opening tag of an element (its attributes).
func head(el string) string {
	braces := 0
	for i, r := range el {
		switch r {
		case '{':
			braces++
		case '}':
			braces--
		case '>':
			if braces == 0 {
				return el[:i+1]
			}
		}
	}
	return el
}

// element returns the JSX element starting at i: a self-closing <X … /> or
// <X …>…</X> including nested elements of the same name.
func element(src string, i int, name string) string {
	hd := head(src[i:])
	if strings.HasSuffix(hd, "/>") {
		return hd
	}
	open, close := "<"+name, "</"+name+">"
	depth := 1
	for j := i + len(hd); j < len(src); j++ {
		switch {
		case strings.HasPrefix(src[j:], close):
			depth--
			if depth == 0 {
				return src[i : j+len(close)]
			}
		case strings.HasPrefix(src[j:], open) && j+len(open) < len(src) && !isIdent(src[j+len(open)]):
			if !strings.HasSuffix(head(src[j:]), "/>") {
				depth++
			}
		}
	}
	return src[i:]
}

// attr returns the raw value of attribute name in an opening tag ({…} balanced, or "…").
func attr(hd, name string) string {
	i := strings.Index(hd, " "+name+"=")
	if i < 0 {
		i = strings.Index(hd, "\n"+name+"=")
	}
	if i < 0 {
		return ""
	}
	v := hd[i+len(name)+2:]
	if strings.HasPrefix(v, "\"") {
		if j := strings.Index(v[1:], "\""); j >= 0 {
			return v[1 : j+1]
		}
		return ""
	}
	depth := 0
	for k, r := range v {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return v[1:k]
			}
		}
	}
	return ""
}

// each calls fn for every <name …> element in src.
func each(src, name string, fn func(start int, el string)) {
	for off := 0; ; {
		i := strings.Index(src[off:], "<"+name)
		if i < 0 {
			return
		}
		i += off
		off = i + len(name) + 1
		if off < len(src) && isIdent(src[off]) {
			continue // <FieldSet …
		}
		fn(i, element(src, i, name))
	}
}

func parseTSX(root, rel string) *tsxFile {
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return nil
	}
	src := string(b)
	f := &tsxFile{path: rel, renders: map[string]string{}, openers: map[string][]opener{}}
	locs := compRe.FindAllStringSubmatchIndex(src, -1)
	for i, l := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		c := &component{name: src[l[2]:l[3]], file: rel}
		body := src[l[0]:end]
		props := propsOf(body)
		for _, name := range []string{"Field", "Checkbox", "Switch"} {
			each(body, name, func(start int, el string) {
				if ff, ok := fieldOf(name, resolveLabel(el, body)); ok {
					// {product && <Checkbox …/>}: only when an existing record is edited
					ff.editOnly = openCond(body, start, props)
					ff.tab = tabOf(body, start)
					c.fields = append(c.fields, ff)
				}
			})
		}
		for _, m := range childRe.FindAllStringSubmatch(body, -1) {
			c.children = appendUniq(c.children, m[1])
		}
		for _, m := range hookUseRe.FindAllStringSubmatch(body, -1) {
			if m[1] != c.name {
				c.hooks = appendUniq(c.hooks, m[1])
			}
		}
		for _, m := range apiPathRe.FindAllStringSubmatch(body, -1) {
			method := map[string]string{"get": "GET", "post": "POST", "put": "PUT", "del": "DELETE"}[m[1]]
			c.calls = append(c.calls, method+" "+strings.SplitN(tmplRe.ReplaceAllString(m[2], "{}"), "?", 2)[0])
		}
		each(body, "Dialog", func(_ int, el string) {
			c.hasDialog = true
			title := attr(head(el), "title")
			for _, k := range tKeyRe.FindAllStringSubmatch(title, -1) {
				key := k[1]
				// `${t('payroll.editEmployee')} — ${e.full_name}`: the rendered title carries data,
				// so the dialog has no stable tag — that branch is "the dialog that opened"
				if strings.Contains(title, "`") && editKeyRe.MatchString(key) {
					key = "*"
				}
				c.dialogKeys = appendUniq(c.dialogKeys, key)
			}
		})
		f.components = append(f.components, c)
	}
	for _, m := range renderRe.FindAllStringSubmatch(src, -1) {
		if _, ok := f.renders[m[1]]; !ok {
			f.renders[m[1]] = m[2]
		}
	}
	each(src, "Button", func(_ int, el string) {
		hd := head(el)
		k := tKeyRe.FindStringSubmatch(el[len(hd):])
		if k == nil {
			return
		}
		for _, m := range setCallRe.FindAllStringSubmatch(attr(hd, "onClick"), -1) {
			v := strings.ToLower(m[1][:1]) + m[1][1:]
			op := opener{file: rel, key: k[1], arg: m[2]}
			if o := objLitRe.FindStringSubmatch(m[2]); o != nil {
				op.discr = [2]string{o[1], o[2]}
			}
			f.openers[v] = append(f.openers[v], op)
		}
	})
	for _, loc := range rowClickRe.FindAllStringIndex(src, -1) {
		hd := "<x " + src[loc[0]:min(len(src), loc[1]+200)]
		val := attr(hd, "onRowClick")
		vars := map[string]bool{}
		for _, m := range setCallRe.FindAllStringSubmatch(val, -1) {
			vars[strings.ToLower(m[1][:1])+m[1][1:]] = true
		}
		if m := setRefRe.FindStringSubmatch("onRowClick={" + val + "}"); m != nil {
			vars[strings.ToLower(m[1][:1])+m[1][1:]] = true
		}
		for v := range vars {
			f.openers[v] = append(f.openers[v], opener{file: rel, arg: "row"})
		}
	}
	for _, m := range importRe.FindAllStringSubmatch(src, -1) {
		if p := resolveImport(rel, m[1]); p != "" {
			f.imports = append(f.imports, p)
		}
	}
	return f
}

// openCond: the control at pos sits inside a still-open {record && …} where record is
// one of the component's props (the edited record), i.e. it is not on the create form.
func openCond(body string, pos int, props []string) bool {
	for _, m := range condAnyRe.FindAllStringSubmatchIndex(body[:pos], -1) {
		if !contains(props, body[m[2]:m[3]]) {
			continue
		}
		braces := 1
		for _, r := range body[m[1]:pos] {
			if r == '{' {
				braces++
			} else if r == '}' {
				braces--
			}
		}
		if braces > 0 {
			return true
		}
	}
	return false
}

var condAnyRe = regexp.MustCompile(`\{\s*(\w+)\s*&&`)

var tabsRe = regexp.MustCompile(`<Tabs\s+value=\{(\w+)\}[^>]*?tabs=\{\[([^\]]*)\]`)
var tabKeyRe = regexp.MustCompile(`key:\s*'([\w-]+)'`)
var tabCondRe = regexp.MustCompile(`\{\s*(\w+)\s*===\s*'([\w-]+)'\s*(?:&&|\?)`)

// tabOf: the tab a control at pos is shown on — inside a still-open
// {mode === 'party' && …} or {mode === 'party' ? (…) : (…)} whose variable is the
// value of a <Tabs> in the same component (the else branch is the other tab).
func tabOf(body string, pos int) string {
	keys := map[string][]string{}
	for _, m := range tabsRe.FindAllStringSubmatch(body, -1) {
		for _, k := range tabKeyRe.FindAllStringSubmatch(m[2], -1) {
			keys[m[1]] = append(keys[m[1]], k[1])
		}
	}
	if len(keys) == 0 {
		return ""
	}
	tab := ""
	for _, m := range tabCondRe.FindAllStringSubmatchIndex(body[:pos], -1) {
		v, key := body[m[2]:m[3]], body[m[4]:m[5]]
		if keys[v] == nil {
			continue
		}
		braces, parens, elseBranch := 1, 0, false
		for _, r := range body[m[1]:pos] {
			switch r {
			case '{':
				braces++
			case '}':
				braces--
			case '(':
				parens++
			case ')':
				parens--
			case ':':
				if braces == 1 && parens == 0 {
					elseBranch = true
				}
			}
		}
		if braces <= 0 {
			continue // that condition closed before the control
		}
		tab = key
		if elseBranch {
			tab = ""
			for _, k := range keys[v] {
				if k != key {
					tab = k
					break
				}
			}
		}
	}
	return tab
}

var labelVarRe = regexp.MustCompile(`\blabel=\{\s*(\w+)\s*\}`)

// resolveLabel inlines a label held in a variable (label={partyLabel} with
// const partyLabel = kind === 'receipt' ? t('treasury.payer') : t('treasury.payee')).
func resolveLabel(el, body string) string {
	m := labelVarRe.FindStringSubmatchIndex(el)
	if m == nil {
		return el
	}
	name := el[m[2]:m[3]]
	def := regexp.MustCompile(`(?:const|let)\s+` + name + `\s*=\s*([^;\n]+)`).FindStringSubmatch(body)
	if def == nil || !strings.Contains(def[1], "t(") {
		return el
	}
	return el[:m[0]] + "label={" + def[1] + "}" + el[m[1]:]
}

// propsOf: the destructured props of a component ({ product, onClose } → product, onClose).
func propsOf(body string) []string {
	m := propsRe.FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	var out []string
	for _, p := range strings.Split(m[1], ",") {
		p = strings.TrimSpace(strings.SplitN(p, "=", 2)[0])
		if i := strings.Index(p, ":"); i >= 0 { // { employee: e } → the local name e
			p = strings.TrimSpace(p[i+1:])
		}
		if p != "" && !strings.HasPrefix(p, "on") {
			out = append(out, p)
		}
	}
	return out
}

func fieldOf(name, el string) (formField, bool) {
	hd := head(el)
	key := ""
	var alts []string
	if t := tagAttrRe.FindStringSubmatch(hd); t != nil {
		key = t[1]
	} else if ks := tKeyRe.FindAllStringSubmatch(attr(hd, "label"), -1); len(ks) > 0 {
		key = ks[0][1]
		for _, k := range ks[1:] {
			if k[1] != key {
				alts = appendUniq(alts, k[1])
			}
		}
	}
	if key == "" {
		return formField{}, false
	}
	ff := formField{key: key, alts: alts, control: "fill"}
	if name != "Field" {
		ff.control = "check"
	} else if c := controlRe.FindStringSubmatch(el[len(hd):]); c != nil {
		ff.control = controlOf(c[1])
	}
	if m := lineSetterRe.FindStringSubmatch(el); m != nil {
		ff.field, ff.line = m[1], true
	}
	for _, re := range setterRes {
		if ff.field != "" {
			break
		}
		if m := re.FindStringSubmatch(el); m != nil {
			ff.field = m[1]
		}
	}
	return ff, ff.field != ""
}

func resolveImport(from, spec string) string {
	var p string
	switch {
	case strings.HasPrefix(spec, "@/"):
		p = strings.TrimPrefix(spec, "@/")
	case strings.HasPrefix(spec, "."):
		p = filepath.ToSlash(filepath.Join(filepath.Dir(from), spec))
	default:
		return ""
	}
	if !strings.HasPrefix(p, "features/") {
		return ""
	}
	return p + ".tsx"
}

func appendUniq(xs []string, s string) []string {
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	return append(xs, s)
}

// frontendForms holds the parsed feature files and the routing table.
type frontendForms struct {
	byName    map[string][]*component
	files     map[string]*tsxFile
	routes    map[string][]string // page file → route templates
	importers map[string][]string // file → files that import it
}

func loadFrontend(repo string) *frontendForms {
	root := filepath.Join(repo, "frontend", "src")
	ff := &frontendForms{files: map[string]*tsxFile{}, routes: map[string][]string{}, importers: map[string][]string{}}
	_ = filepath.Walk(filepath.Join(root, "features"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".tsx") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if f := parseTSX(root, rel); f != nil {
			ff.files[rel] = f
		}
		return nil
	})
	for rel, f := range ff.files {
		for _, imp := range f.imports {
			ff.importers[imp] = appendUniq(ff.importers[imp], rel)
		}
	}
	// a component sends the requests of the hooks it uses (useCheckForm → POST /checks)
	byName := map[string][]*component{}
	for _, rel := range sortedKeys(ff.files) {
		for _, c := range ff.files[rel].components {
			byName[c.name] = append(byName[c.name], c)
		}
	}
	for _, rel := range sortedKeys(ff.files) {
		f := ff.files[rel]
		for _, c := range f.components {
			for _, h := range c.hooks {
				for _, hc := range byName[h] {
					if hc.file == rel || contains(f.imports, hc.file) {
						c.calls = append(c.calls, hc.calls...)
					}
				}
			}
		}
	}
	ff.byName = byName
	app, _ := os.ReadFile(filepath.Join(root, "App.tsx"))
	comp := map[string]string{} // component → file
	for _, m := range lazyRe.FindAllStringSubmatch(string(app), -1) {
		comp[m[1]] = strings.TrimPrefix(m[2], "@/") + ".tsx"
	}
	for _, m := range directImpRe.FindAllStringSubmatch(string(app), -1) {
		for _, n := range strings.Split(m[1], ",") {
			comp[strings.TrimSpace(n)] = strings.TrimPrefix(m[2], "@/") + ".tsx"
		}
	}
	for _, m := range routeRe.FindAllStringSubmatch(string(app), -1) {
		if file, ok := comp[m[2]]; ok {
			ff.routes[file] = append(ff.routes[file], "/"+m[1])
		}
	}
	return ff
}

// pageRoutes: the routes of the pages that render file (itself, or pages importing it).
func (ff *frontendForms) pageRoutes(file string) []string {
	var out []string
	seen := map[string]bool{}
	queue := []string{file}
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		if seen[f] {
			continue
		}
		seen[f] = true
		if rs := ff.routes[f]; len(rs) > 0 {
			out = append(out, rs...)
			continue
		}
		queue = append(queue, ff.importers[f]...)
	}
	sort.Strings(out)
	return out
}

var newKeyRe = regexp.MustCompile(`(?i)\.(new|add|create)\w*$|\.\w*(New|Add|Create)\w*$`)
var editKeyRe = regexp.MustCompile(`(?i)\.(edit)\w*$|\.\w*Edit\w*$`)

func pick(routes []string, ok func(string) bool) string {
	best := ""
	for _, r := range routes {
		if ok(r) && (best == "" || len(r) < len(best)) {
			best = r
		}
	}
	return best
}

// unreachableForms: forms the live check (widget/scripts/verify-forms.mjs) showed the
// generator cannot open, with the reason. The audit reports them as missing UI
// capability; keep this short and remove entries when the generator learns the pattern.
var unreachableForms = map[string]string{
	"expenses.categories.create": "the category form is nested inside the categories manager dialog (a second opener inside a dialog)",
}

// formFor finds the real form for a create/update operation.
func (ff *frontendForms) formFor(op *Operation) *UIForm {
	if ff == nil || (op.Kind != "create" && op.Kind != "update") || len(op.UIEntryPoints) == 0 {
		return nil
	}
	if _, ok := unreachableForms[op.ID]; ok {
		return nil
	}
	writable := map[string]bool{}
	for _, f := range op.Body {
		if !responseOnly[f.Name] {
			writable[f.Name] = true
		}
	}
	// the component in a calling file whose fields best match the request; when some
	// component sends exactly this request, only those are candidates
	var cands []*component
	for _, rel := range op.UIEntryPoints {
		if f := ff.files[rel]; f != nil {
			cands = append(cands, f.components...)
		}
	}
	// senders: any component that sends this request, itself or through a hook it uses
	var all []*component
	for _, rel := range sortedKeys(ff.files) {
		all = append(all, ff.files[rel].components...)
	}
	var senders []*component
	for _, c := range all {
		for _, call := range c.calls {
			if m, p, _ := strings.Cut(call, " "); m == op.Method && pathMatch(op.Path, p) {
				senders = append(senders, c)
				break
			}
		}
	}
	if len(senders) > 0 {
		cands = senders
	}
	type cand struct {
		c *component
		n int
	}
	count := func(fields []formField) int {
		n := 0
		for _, x := range fields {
			if writable[x.field] {
				n++
			}
		}
		return n
	}
	var ranked []cand
	for _, c := range cands {
		if n := count(c.fields); n > 0 {
			ranked = append(ranked, cand{c, n})
		}
	}
	// a sender that renders its fields in a child (CheckForm → CheckFormBody)
	for _, c := range senders {
		if count(c.fields) > 0 {
			continue
		}
		for _, ch := range c.children {
			for _, x := range ff.byName[ch] {
				if n := count(x.fields); n > 0 && (x.file == c.file || contains(ff.files[c.file].imports, x.file)) {
					merged := *c
					merged.fields, merged.hasDialog = x.fields, c.hasDialog || x.hasDialog
					merged.dialogKeys = append(append([]string{}, c.dialogKeys...), x.dialogKeys...)
					ranked = append(ranked, cand{&merged, n})
				}
			}
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].n > ranked[j].n })
	// the best-matching form that can actually be opened for this kind of call
	for _, rc := range ranked {
		if form := ff.build(op, rc.c, writable); form != nil {
			return form
		}
	}
	return nil
}

func (ff *frontendForms) build(op *Operation, best *component, writable map[string]bool) *UIForm {
	form := &UIForm{Fields: map[string]UIField{}, File: best.file}
	for _, x := range best.fields {
		switch {
		case x.line:
			if form.Lines == nil {
				form.Lines = map[string]UIField{}
			}
			form.Lines[x.field] = UIField{Tag: x.key, Control: x.control}
		case x.editOnly && op.Kind == "create":
			// not on the create form
		case writable[x.field]:
			if _, dup := form.Fields[x.field]; !dup {
				form.Fields[x.field] = UIField{Tag: x.key, Alts: x.alts, Tab: x.tab, Control: x.control}
			}
		}
	}
	if !ff.place(op, best, form) {
		return nil
	}
	for name := range writable {
		_, a := form.Fields[name]
		_, b := form.Lines[name]
		joined := strings.HasSuffix(name, "_name") && writable[strings.TrimSuffix(name, "_name")+"_id"] // display name of a reference
		if !a && !b && !joined {
			form.Missing = append(form.Missing, name)
		}
	}
	sort.Strings(form.Missing)
	return form
}

// place decides where the form lives and how it opens.
func (ff *frontendForms) place(op *Operation, c *component, form *UIForm) bool {
	if !c.hasDialog {
		// a page form: its own route ("/journal/new", "/journal/:id/edit"), or the
		// settings page itself for a singleton ("/settings/company")
		routes := ff.pageRoutes(c.file)
		switch {
		case op.Kind == "create":
			form.Route = pick(routes, func(r string) bool { return strings.HasSuffix(r, "/new") })
		case strings.Contains(op.Path, "{id}"):
			form.Route = pick(routes, func(r string) bool { return strings.HasSuffix(r, "/:id/edit") })
		default:
			form.Route = pick(routes, func(r string) bool { return !strings.Contains(r, ":") })
		}
		return form.Route != ""
	}
	for _, k := range c.dialogKeys {
		if (op.Kind == "create" && newKeyRe.MatchString(k)) || (op.Kind == "update" && (k == "*" || editKeyRe.MatchString(k))) {
			form.Dialogs = append(form.Dialogs, k)
		}
	}
	if len(form.Dialogs) == 0 {
		form.Dialogs = []string{"*"} // a title made of data (the record's name): the dialog that opened
	}
	// the components that render this form (EmployeeDialog → EmployeeForm), file-aware
	targets := ff.renderers(c, 3)
	body := map[string]bool{}
	for _, f := range op.Body {
		body[f.Name] = true
	}
	// the files that render one of them under a state variable, and the controls that set it
	for _, rel := range sortedKeys(ff.files) {
		f := ff.files[rel]
		for _, v := range sortedKeys(f.renders) {
			comp := f.renders[v]
			if !targets[rel+"#"+comp] {
				continue
			}
			for _, o := range f.openers[v] {
				routes := ff.pageRoutes(rel)
				detail := pick(routes, func(r string) bool { return strings.HasSuffix(r, "/:id") })
				list := pick(routes, func(r string) bool { return !strings.Contains(r, ":") })
				if o.discr[0] != "" && !body[o.discr[0]] {
					o.discr = [2]string{} // a UI mode ({ mode: 'create' }), not a request field
					o.arg = "{}"
				}
				isNew := o.arg == "'new'" || o.arg == "true" && detail == "" || o.arg == "{}" || o.discr[0] != ""
				switch {
				case op.Kind == "create" && isNew && list != "":
					if form.Route == "" || form.Route == list {
						form.Route = list
						form.Open = appendOpen(form.Open, UIOpen{Tag: o.key, Field: o.discr[0], Value: o.discr[1]})
					}
				case op.Kind == "update" && o.arg == "row" && list != "":
					form.Route, form.Row = list, true
				case op.Kind == "update" && o.key != "" && !isNew && detail != "" && !form.Row:
					form.Route = detail
					form.Open = appendOpen(form.Open, UIOpen{Tag: o.key})
				}
			}
		}
	}
	return form.Route != "" && (len(form.Open) > 0 || form.Row)
}

// renderers: "file#Component" keys under which the form can be rendered — the form
// itself, and components that render it (in its file or a file importing it).
func (ff *frontendForms) renderers(c *component, depth int) map[string]bool {
	out := map[string]bool{}
	type node struct{ file, name string }
	queue := []node{{c.file, c.name}}
	for d := 0; d <= depth && len(queue) > 0; d++ {
		var next []node
		for _, n := range queue {
			// rendered by name in its own file or in a file importing it
			for rel, f := range ff.files {
				if rel != n.file && !contains(f.imports, n.file) {
					continue
				}
				out[rel+"#"+n.name] = true
				for _, x := range f.components {
					if x.name != n.name && contains(x.children, n.name) && !out[rel+"#"+x.name] {
						next = append(next, node{rel, x.name})
					}
				}
			}
		}
		queue = next
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func appendOpen(xs []UIOpen, o UIOpen) []UIOpen {
	for _, x := range xs {
		if x == o {
			return xs
		}
	}
	return append(xs, o)
}

// responseOnly mirrors catalog.readOnlyFields: properties the request struct shares with the response.
var responseOnly = map[string]bool{"id": true, "balance": true, "created_at": true, "updated_at": true, "posted_at": true, "on_hand": true,
	"stock_value": true, "has_logo": true, "has_children": true, "postable": true, "account_code": true}
