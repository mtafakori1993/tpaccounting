// Command capmap discovers the accounting application's capability surface from
// its SOURCE CODE (no assumptions): every HTTP operation the Go server registers,
// its permission, inputs (request struct fields, required ones from validators,
// query parameters), in-handler permission checks and doc comment; and, from the
// React app, which operations the UI actually calls and from which screen.
//
//	go run ./cmd/capmap -repo ../.. -out ../knowledge/capabilities.json -md ../CAPABILITIES.md
//
// The JSON is embedded into the AI service (internal/catalog) and becomes the tool
// registry; the Markdown is the human-readable capability matrix.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Field struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Enum     []string `json:"enum,omitempty"`   // allowed values found in the validators
	Fields   []Field  `json:"fields,omitempty"` // element fields of nested structs / slices of structs
}

type Operation struct {
	ID             string   `json:"id"`
	Module         string   `json:"module"`
	Method         string   `json:"method"`
	Path           string   `json:"path"` // relative to /api/v1
	Handler        string   `json:"handler"`
	Doc            string   `json:"doc,omitempty"`
	Permission     string   `json:"permission,omitempty"`        // all route-level permissions joined with "+"
	InnerPerms     []string `json:"inner_permissions,omitempty"` // checked inside the handler
	Public         bool     `json:"public,omitempty"`
	PathParams     []string `json:"path_params,omitempty"`
	QueryParams    []string `json:"query_params,omitempty"`
	BodyType       string   `json:"body_type,omitempty"`
	Body           []Field  `json:"body,omitempty"`
	Multipart      bool     `json:"multipart,omitempty"`
	Binary         bool     `json:"binary_response,omitempty"`
	Kind           string   `json:"kind"`           // read | create | update | delete | workflow | export | compute
	Action         string   `json:"action"`         // workflow verb / handler action
	Classification string   `json:"classification"` // FULLY_AUTONOMOUS | REQUIRES_USER_CONFIRMATION | REQUIRES_APPROVAL | READ_ONLY | NOT_SUPPORTED_BY_AI | NOT_SAFE_TO_AUTOMATE
	Risk           string   `json:"risk"`           // low | medium | high
	AccountingImp  string   `json:"accounting_impact,omitempty"`
	UIExists       bool     `json:"ui_exists"`
	UIEntryPoints  []string `json:"ui_entry_points,omitempty"`
	Notes          []string `json:"notes,omitempty"`
}

type UICall struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	File   string `json:"file"`
	Match  bool   `json:"matched"`
}

type Catalog struct {
	Generated   string      `json:"generated_by"`
	Operations  []Operation `json:"operations"`
	UnmatchedUI []UICall    `json:"ui_calls_without_backend"`
}

// ---------- package loading ----------

type pkg struct {
	name    string
	files   []*ast.File
	funcs   map[string]*ast.FuncDecl // "Recv.Name" and "Name"
	types   map[string]*ast.TypeSpec
	vars    map[string]ast.Expr // package-level var/const values
	reqs    map[string]bool     // field names validated as required anywhere in the package
	enums   map[string][]string // field name → allowed values (from v.Check(x == "a" || x == "b", "field", …))
	imports map[string]string   // per-package union: alias → dir name
}

var fset = token.NewFileSet()
var pkgs = map[string]*pkg{}
var internalDir string

func loadPkg(dir string) *pkg {
	if p, ok := pkgs[dir]; ok {
		return p
	}
	p := &pkg{name: dir, funcs: map[string]*ast.FuncDecl{}, types: map[string]*ast.TypeSpec{}, vars: map[string]ast.Expr{},
		reqs: map[string]bool{}, enums: map[string][]string{}, imports: map[string]string{}}
	pkgs[dir] = p
	matches, _ := filepath.Glob(filepath.Join(internalDir, dir, "*.go"))
	for _, f := range matches {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ParseComments)
		if err != nil {
			continue
		}
		p.files = append(p.files, af)
		for _, im := range af.Imports {
			path, _ := strconv.Unquote(im.Path.Value)
			alias := filepath.Base(path)
			if im.Name != nil {
				alias = im.Name.Name
			}
			if strings.Contains(path, "/internal/") {
				p.imports[alias] = strings.SplitN(path, "/internal/", 2)[1]
			}
		}
		for _, d := range af.Decls {
			switch x := d.(type) {
			case *ast.FuncDecl:
				key := x.Name.Name
				if x.Recv != nil && len(x.Recv.List) > 0 {
					key = recvName(x.Recv.List[0].Type) + "." + key
				}
				p.funcs[key] = x
			case *ast.GenDecl:
				for _, s := range x.Specs {
					switch sp := s.(type) {
					case *ast.TypeSpec:
						p.types[sp.Name.Name] = sp
					case *ast.ValueSpec:
						for i, n := range sp.Names {
							if i < len(sp.Values) {
								p.vars[n.Name] = sp.Values[i]
							}
						}
					}
				}
			}
		}
		ast.Inspect(af, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
				switch sel.Sel.Name {
				case "Required", "RequiredID":
					if len(c.Args) > 0 {
						if s, ok := strLit(c.Args[0]); ok {
							p.reqs[s] = true
						}
					}
				case "Check":
					if len(c.Args) == 3 {
						if field, ok := strLit(c.Args[1]); ok {
							if vals := enumValues(c.Args[0]); len(vals) > 1 {
								p.enums[field] = mergeEnum(p.enums[field], vals)
							}
						}
					}
					if len(c.Args) == 3 {
						if code, ok := strLit(c.Args[2]); ok && code == "required" {
							if s, ok := strLit(c.Args[1]); ok {
								p.reqs[s] = true
							}
						}
					}
				}
			}
			return true
		})
	}
	return p
}

func recvName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return recvName(x.X)
	case *ast.Ident:
		return x.Name
	}
	return ""
}

func strLit(e ast.Expr) (string, bool) {
	if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
		s, err := strconv.Unquote(b.Value)
		return s, err == nil
	}
	return "", false
}

// ---------- route interpretation ----------

type env struct {
	p      *pkg
	vars   map[string]string // string-valued locals (paths, loop vars, permission ids)
	perms  map[string]string // middleware locals → permission
	pfuncs map[string]string // closures returning Require(prefix + "." + arg) → prefix
	recv   map[string]string // receiver composite-literal fields (Module, Side)
	objs   map[string]objRef // locals bound to &pkg.Type{...}
	module string
}

type objRef struct {
	dir, typ string
	fields   map[string]string
}

func (e *env) clone() *env {
	n := &env{p: e.p, vars: map[string]string{}, perms: map[string]string{}, pfuncs: map[string]string{}, recv: e.recv, objs: e.objs, module: e.module}
	for k, v := range e.vars {
		n.vars[k] = v
	}
	for k, v := range e.perms {
		n.perms[k] = v
	}
	for k, v := range e.pfuncs {
		n.pfuncs[k] = v
	}
	return n
}

// evalStr evaluates a string expression (literals, concatenation, locals, receiver fields).
func (e *env) evalStr(x ast.Expr) (string, bool) {
	switch v := x.(type) {
	case *ast.BasicLit:
		return strLit(v)
	case *ast.Ident:
		s, ok := e.vars[v.Name]
		return s, ok
	case *ast.BinaryExpr:
		if v.Op == token.ADD {
			a, ok1 := e.evalStr(v.X)
			b, ok2 := e.evalStr(v.Y)
			return a + b, ok1 && ok2
		}
	case *ast.SelectorExpr:
		if s, ok := e.recv[v.Sel.Name]; ok {
			return s, true
		}
	case *ast.ParenExpr:
		return e.evalStr(v.X)
	}
	return "", false
}

// permOf resolves a middleware expression to a permission id.
func (e *env) permOf(x ast.Expr) string {
	switch v := x.(type) {
	case *ast.Ident:
		return e.perms[v.Name]
	case *ast.CallExpr:
		if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Require" && len(v.Args) == 1 {
			s, _ := e.evalStr(v.Args[0])
			return s
		}
		if id, ok := v.Fun.(*ast.Ident); ok && len(v.Args) == 1 {
			if prefix, ok := e.pfuncs[id.Name]; ok {
				a, _ := e.evalStr(v.Args[0])
				return prefix + "." + a
			}
		}
	}
	return ""
}

var ops []Operation

func (e *env) walk(prefix string, stmts []ast.Stmt, inherited string) {
	for _, st := range stmts {
		switch s := st.(type) {
		case *ast.AssignStmt:
			for i, l := range s.Lhs {
				id, ok := l.(*ast.Ident)
				if !ok || i >= len(s.Rhs) {
					continue
				}
				rhs := s.Rhs[i]
				if p := e.permOf(rhs); p != "" {
					e.perms[id.Name] = p
					continue
				}
				if fl, ok := rhs.(*ast.FuncLit); ok {
					// perm := func(a string) ... { return identity.Require(h.Module + "." + a) }
					ast.Inspect(fl.Body, func(n ast.Node) bool {
						if c, ok := n.(*ast.CallExpr); ok {
							if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Require" && len(c.Args) == 1 {
								if be, ok := c.Args[0].(*ast.BinaryExpr); ok {
									if inner, ok := be.X.(*ast.BinaryExpr); ok {
										if p, ok := e.evalStr(inner.X); ok {
											e.pfuncs[id.Name] = p
										}
									}
								}
							}
						}
						return true
					})
					continue
				}
				if s2, ok := e.evalStr(rhs); ok {
					e.vars[id.Name] = s2
					continue
				}
				if o, ok := e.objOf(rhs); ok {
					e.objs[id.Name] = o
				}
			}
		case *ast.ExprStmt:
			if c, ok := s.X.(*ast.CallExpr); ok {
				e.call(prefix, c, inherited)
			}
		case *ast.RangeStmt:
			var keys []string
			switch r := s.X.(type) {
			case *ast.CompositeLit:
				for _, el := range r.Elts {
					if str, ok := e.evalStr(el); ok {
						keys = append(keys, str)
					}
				}
			case *ast.Ident:
				if v, ok := e.p.vars[r.Name]; ok {
					if cl, ok := v.(*ast.CompositeLit); ok {
						for _, el := range cl.Elts {
							if kv, ok := el.(*ast.KeyValueExpr); ok {
								if str, ok := strLit(kv.Key); ok {
									keys = append(keys, str)
								}
							} else if str, ok := strLit(el); ok {
								keys = append(keys, str)
							}
						}
					}
				}
			}
			sort.Strings(keys)
			loopVar := ""
			if s.Value != nil {
				loopVar = s.Value.(*ast.Ident).Name
			} else if s.Key != nil {
				loopVar = s.Key.(*ast.Ident).Name
			}
			for _, k := range keys {
				n := e.clone()
				n.vars[loopVar] = k
				n.walk(prefix, s.Body.List, inherited)
			}
		case *ast.BlockStmt:
			e.walk(prefix, s.List, inherited)
		}
	}
}

func (e *env) objOf(x ast.Expr) (objRef, bool) {
	if u, ok := x.(*ast.UnaryExpr); ok {
		x = u.X
	}
	if c, ok := x.(*ast.CallExpr); ok { // pkg.NewService(...) → *pkg.Service
		if sel, ok := c.Fun.(*ast.SelectorExpr); ok && strings.HasPrefix(sel.Sel.Name, "New") {
			if id, ok := sel.X.(*ast.Ident); ok {
				if dir, ok := e.p.imports[id.Name]; ok {
					return objRef{dir: dir, typ: strings.TrimPrefix(sel.Sel.Name, "New"), fields: map[string]string{}}, true
				}
			}
		}
	}
	cl, ok := x.(*ast.CompositeLit)
	if !ok {
		return objRef{}, false
	}
	sel, ok := cl.Type.(*ast.SelectorExpr)
	if !ok {
		return objRef{}, false
	}
	dir := e.p.imports[sel.X.(*ast.Ident).Name]
	o := objRef{dir: dir, typ: sel.Sel.Name, fields: map[string]string{}}
	for _, el := range cl.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			k := kv.Key.(*ast.Ident).Name
			if s, ok := strLit(kv.Value); ok {
				o.fields[k] = s
			} else if se, ok := kv.Value.(*ast.SelectorExpr); ok {
				o.fields[k] = se.Sel.Name // e.g. invoices.SideSales
			}
		}
	}
	return o, true
}

var methods = map[string]string{"Get": "GET", "Post": "POST", "Put": "PUT", "Delete": "DELETE", "Patch": "PATCH"}

// call interprets r.With(..).Get(...), r.Route(...), r.Group(...), x.Routes(r) etc.
func (e *env) call(prefix string, c *ast.CallExpr, inherited string) {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	perm := inherited
	recvExpr := sel.X
	// r.With(m1, m2).Method(...): every Require applies
	if wc, ok := recvExpr.(*ast.CallExpr); ok {
		if ws, ok := wc.Fun.(*ast.SelectorExpr); ok && ws.Sel.Name == "With" {
			var ps []string
			if inherited != "" {
				ps = append(ps, inherited)
			}
			for _, a := range wc.Args {
				if p := e.permOf(a); p != "" {
					ps = append(ps, p)
				}
			}
			perm = strings.Join(ps, "+")
		}
	}
	name := sel.Sel.Name
	switch {
	case methods[name] != "" && len(c.Args) == 2:
		path, ok := e.evalStr(c.Args[0])
		if !ok {
			return
		}
		e.addOp(methods[name], prefix+path, c.Args[1], perm)
	case name == "Use" && len(c.Args) > 0:
		for _, a := range c.Args {
			if p := e.permOf(a); p != "" {
				e.perms["__use"] = p
			}
		}
	case name == "Route" && len(c.Args) == 2:
		path, ok := e.evalStr(c.Args[0])
		if !ok {
			return
		}
		e.into(prefix+path, c.Args[1], perm)
	case name == "Group" && len(c.Args) == 1:
		e.into(prefix, c.Args[0], perm)
	default:
		// handler.Routes(r) style
		if len(c.Args) == 1 {
			e.into(prefix, &ast.SelectorExpr{X: sel.X, Sel: sel.Sel}, perm)
		}
	}
}

// into follows a router function: a literal, or a method value like (&pkg.T{..}).Routes / x.Routes.
func (e *env) into(prefix string, fn ast.Expr, perm string) {
	switch f := fn.(type) {
	case *ast.FuncLit:
		n := e.clone()
		if len(f.Type.Params.List) > 0 && len(f.Type.Params.List[0].Names) > 0 {
			_ = f.Type.Params.List[0].Names[0].Name
		}
		n.walk(prefix, f.Body.List, perm)
		if p := n.perms["__use"]; p != "" {
			// a Group with r.Use(Require(..)) applies to its routes — re-walk with it
			m := e.clone()
			m.walk(prefix, f.Body.List, p)
		}
	case *ast.SelectorExpr:
		var o objRef
		var ok bool
		switch x := f.X.(type) {
		case *ast.ParenExpr:
			o, ok = e.objOf(x.X)
		case *ast.Ident:
			o, ok = e.objs[x.Name]
		}
		if !ok || o.dir == "" {
			return
		}
		p := loadPkg(o.dir)
		fd := p.funcs[o.typ+"."+f.Sel.Name]
		if fd == nil {
			return
		}
		n := &env{p: p, vars: map[string]string{}, perms: map[string]string{}, pfuncs: map[string]string{}, recv: o.fields, objs: map[string]objRef{}, module: o.dir}
		n.walk(prefix, fd.Body.List, perm)
	}
}

func (e *env) addOp(method, path string, h ast.Expr, perm string) {
	// handler: httpx.Handler(h.fn) | httpx.Handler(h.fn(arg)) | h.fn
	if c, ok := h.(*ast.CallExpr); ok {
		if s, ok := c.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Handler" && len(c.Args) == 1 {
			h = c.Args[0]
		}
	}
	hname, harg := "", ""
	var fd *ast.FuncDecl
	switch x := h.(type) {
	case *ast.SelectorExpr:
		hname = x.Sel.Name
	case *ast.CallExpr:
		if s, ok := x.Fun.(*ast.SelectorExpr); ok {
			hname = s.Sel.Name
			if len(x.Args) > 0 {
				harg, _ = e.evalStr(x.Args[0])
			}
		}
	case *ast.FuncLit:
		hname = "inline"
	}
	for k, f := range e.p.funcs {
		if strings.HasSuffix(k, "."+hname) || k == hname {
			fd = f
			break
		}
	}
	op := Operation{Method: method, Path: path, Handler: hname, Permission: perm, Module: e.module}
	if harg != "" {
		op.Action = harg
	}
	if fd != nil {
		e.inspectHandler(&op, fd)
	}
	ops = append(ops, op)
}

var queryFns = map[string]bool{"QueryInt64": true, "QueryDate": true, "QueryBool": true, "QueryInt": true}

func (e *env) inspectHandler(op *Operation, fd *ast.FuncDecl) {
	if fd.Doc != nil {
		op.Doc = strings.TrimSpace(strings.Split(fd.Doc.Text(), "\n\n")[0])
		op.Doc = strings.ReplaceAll(op.Doc, "\n", " ")
	}
	seen := map[*ast.FuncDecl]bool{}
	var visit func(body *ast.BlockStmt, depth int)
	q := map[string]bool{}
	locals := map[string]ast.Expr{} // var name → type expr
	visit = func(body *ast.BlockStmt, depth int) {
		ast.Inspect(body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.ValueSpec:
				for _, nm := range x.Names {
					if x.Type != nil {
						locals[nm.Name] = x.Type
					}
				}
			case *ast.AssignStmt:
				for i, l := range x.Lhs {
					if id, ok := l.(*ast.Ident); ok && i < len(x.Rhs) {
						if cl, ok := x.Rhs[i].(*ast.CompositeLit); ok && cl.Type != nil {
							locals[id.Name] = cl.Type
						}
					}
				}
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if ok {
					switch {
					case sel.Sel.Name == "Decode" && len(x.Args) == 2:
						if u, ok := x.Args[1].(*ast.UnaryExpr); ok {
							if id, ok := u.X.(*ast.Ident); ok {
								if t, ok := locals[id.Name]; ok {
									op.BodyType = typeString(t)
									op.Body = e.fieldsOf(t, 0)
								}
							}
						}
					case sel.Sel.Name == "Get" && len(x.Args) == 1:
						if s, ok := strLit(x.Args[0]); ok && isQueryRecv(sel.X) {
							q[s] = true
						}
					case queryFns[sel.Sel.Name] && len(x.Args) == 2:
						if s, ok := strLit(x.Args[1]); ok {
							q[s] = true
						}
					case sel.Sel.Name == "ParseList":
						for _, s := range []string{"page", "page_size", "sort", "order", "q"} {
							q[s] = true
						}
					case sel.Sel.Name == "Check" && len(x.Args) == 2:
						if s, ok := strLit(x.Args[1]); ok {
							op.InnerPerms = append(op.InnerPerms, s)
						}
					case sel.Sel.Name == "ParseMultipartForm" || sel.Sel.Name == "FormFile":
						op.Multipart = true
					case sel.Sel.Name == "FormValue" && len(x.Args) == 1:
						if s, ok := strLit(x.Args[0]); ok {
							op.Body = appendField(op.Body, Field{Name: s, Type: "form"})
						}
					case sel.Sel.Name == "Set" && len(x.Args) == 2:
						if s, ok := strLit(x.Args[0]); ok && s == "Content-Disposition" {
							op.Binary = true
						}
					}
				}
				if id, ok := x.Fun.(*ast.Ident); ok && depth < 2 {
					// follow same-package helpers that receive the request (e.g. parseFilter(r))
					if f := e.p.funcs[id.Name]; f != nil && !seen[f] && f.Body != nil {
						seen[f] = true
						visit(f.Body, depth+1)
					}
					if strings.HasPrefix(strings.ToLower(id.Name), "write") || strings.Contains(id.Name, "PDF") {
						op.Binary = op.Binary || strings.Contains(strings.ToLower(id.Name), "pdf") || strings.Contains(strings.ToLower(id.Name), "file")
					}
				}
			}
			return true
		})
	}
	seen[fd] = true
	visit(fd.Body, 0)
	for k := range q {
		op.QueryParams = append(op.QueryParams, k)
	}
	sort.Strings(op.QueryParams)
	sort.Strings(op.InnerPerms)
}

func appendField(fs []Field, f Field) []Field {
	for _, x := range fs {
		if x.Name == f.Name {
			return fs
		}
	}
	return append(fs, f)
}

func isQueryRecv(x ast.Expr) bool {
	switch v := x.(type) {
	case *ast.Ident:
		return v.Name == "q" || v.Name == "qv" || v.Name == "query"
	case *ast.CallExpr:
		if s, ok := v.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Query" {
			return true
		}
	}
	return false
}

func typeString(t ast.Expr) string {
	switch x := t.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return typeString(x.X) + "." + x.Sel.Name
	case *ast.StarExpr:
		return "*" + typeString(x.X)
	case *ast.ArrayType:
		return "[]" + typeString(x.Elt)
	case *ast.MapType:
		return "map[" + typeString(x.Key) + "]" + typeString(x.Value)
	}
	return "?"
}

// fieldsOf lists the JSON fields of a struct type (resolving other internal packages), 2 levels deep.
func (e *env) fieldsOf(t ast.Expr, depth int) []Field {
	p := e.p
	var spec *ast.TypeSpec
	switch x := t.(type) {
	case *ast.Ident:
		spec = p.types[x.Name]
	case *ast.SelectorExpr:
		if dir, ok := p.imports[x.X.(*ast.Ident).Name]; ok {
			p = loadPkg(dir)
			spec = p.types[x.Sel.Name]
		}
	case *ast.StarExpr:
		return e.fieldsOf(x.X, depth)
	case *ast.ArrayType:
		return e.fieldsOf(x.Elt, depth)
	}
	if spec == nil {
		return nil
	}
	st, ok := spec.Type.(*ast.StructType)
	if !ok {
		return nil
	}
	sub := &env{p: p}
	var out []Field
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 { // embedded
			out = append(out, sub.fieldsOf(f.Type, depth)...)
			continue
		}
		name := f.Names[0].Name
		if f.Tag != nil {
			tag, _ := strconv.Unquote(f.Tag.Value)
			if m := regexp.MustCompile(`json:"([^",]*)`).FindStringSubmatch(tag); m != nil {
				if m[1] == "-" {
					continue
				}
				if m[1] != "" {
					name = m[1]
				}
			}
		}
		if !ast.IsExported(f.Names[0].Name) {
			continue
		}
		fld := Field{Name: name, Type: typeString(f.Type), Required: p.reqs[name], Enum: p.enums[name]}
		if depth < 2 {
			if _, isBasic := basic[strings.TrimLeft(fld.Type, "*[]")]; !isBasic {
				fld.Fields = sub.fieldsOf(f.Type, depth+1)
			}
		}
		out = append(out, fld)
	}
	return out
}

var basic = map[string]bool{"string": true, "int": true, "int64": true, "bool": true, "float64": true, "decimal.Decimal": true,
	"civil.Date": true, "time.Time": true, "json.RawMessage": true}

// ---------- classification ----------

var notSafeModules = []string{"/users", "/roles", "/settings/", "/company/account-mappings"}
var notSafePaths = regexp.MustCompile(`^/(company/(fiscal-years|periods|logo|$)|company$|payroll/(settings|tax-tables|insurance-rules)|budgets/control/rules)`)

func classify(op *Operation) {
	p, m := op.Path, op.Method
	last := p[strings.LastIndex(p, "/")+1:]
	verb := strings.ToLower(op.Handler)
	switch {
	case m == "GET" && (strings.Contains(p, "export") || op.Binary || strings.Contains(p, "template")):
		op.Kind = "export"
	case m == "GET":
		op.Kind = "read"
	case m == "DELETE":
		op.Kind = "delete"
	case m == "PUT":
		op.Kind = "update"
	case m == "POST" && (verb == "check" || verb == "preview" || verb == "render"):
		op.Kind = "compute"
	case m == "POST" && strings.HasPrefix(last, "{") || m == "POST" && !strings.Contains(p, "{"):
		op.Kind = "create"
	default:
		op.Kind = "workflow"
	}
	if op.Action == "" {
		op.Action = op.Handler
		if op.Kind == "workflow" && !strings.HasPrefix(last, "{") {
			op.Action = last
		}
	}
	for _, s := range strings.Split(p, "/") {
		if strings.HasPrefix(s, "{") {
			op.PathParams = append(op.PathParams, strings.Trim(s, "{}"))
		}
	}
	act := strings.ToLower(op.Action + " " + last)
	destructive := op.Kind == "delete" || strings.Contains(act, "cancel") || strings.Contains(act, "unpost") || strings.Contains(act, "void") ||
		strings.Contains(act, "revers") || strings.Contains(act, "reopen") || strings.Contains(act, "close") || strings.Contains(act, "archive")
	switch {
	case strings.HasPrefix(p, "/auth") || strings.HasPrefix(p, "/setup"):
		op.Classification, op.Risk = "NOT_SUPPORTED_BY_AI", "high"
		op.Notes = append(op.Notes, "authentication/setup is never performed by the assistant")
	case op.Kind == "read" || op.Kind == "export" || op.Kind == "compute":
		op.Classification, op.Risk = "READ_ONLY", "low"
	case notSafePaths.MatchString(p) || hasPrefixAny(p, notSafeModules):
		op.Classification, op.Risk = "NOT_SAFE_TO_AUTOMATE", "high"
		op.Notes = append(op.Notes, "security, configuration or period control: the assistant reads it but a human changes it in the app")
	case destructive:
		op.Classification, op.Risk = "REQUIRES_APPROVAL", "high"
		op.Notes = append(op.Notes, "cancel/delete/reverse: always approved by a human, never automatic")
	case strings.HasPrefix(p, "/payroll") || strings.HasPrefix(p, "/employees") || (strings.HasPrefix(p, "/checks") && op.Kind == "workflow") ||
		(strings.HasPrefix(p, "/budgets") && op.Kind == "workflow"):
		op.Classification, op.Risk = "REQUIRES_APPROVAL", "high"
		op.Notes = append(op.Notes, "payroll, check and budget lifecycle steps are always approved by a human")
	default:
		op.Classification, op.Risk = "REQUIRES_USER_CONFIRMATION", "medium"
	}
	// accounting impact (from the module's automatic accounting, see the accounting model guide)
	switch {
	case op.Kind == "read" || op.Kind == "export" || op.Kind == "compute":
	case strings.HasPrefix(p, "/treasury/transactions"):
		op.AccountingImp = "posts a journal entry (cash/bank vs party or account); cancel = reversal entry"
	case strings.HasPrefix(p, "/expenses") && (op.Action == "post" || op.Kind == "create"):
		op.AccountingImp = "posting books Dr expense / Cr cash, bank or payable"
	case (strings.HasPrefix(p, "/sales") || strings.HasPrefix(p, "/purchases")) && (op.Action == "post" || op.Action == "cancel"):
		op.AccountingImp = "posts/reverses the invoice entry (receivable/payable, revenue/purchases, VAT, COGS/inventory)"
	case strings.HasPrefix(p, "/inventory/documents") && (op.Action == "post" || op.Kind == "create" || op.Action == "cancel"):
		op.AccountingImp = "stock movements at moving-average cost + inventory entry"
	case strings.HasPrefix(p, "/journal-entries"):
		op.AccountingImp = "manual journal entry (draft → post; cancel = reversal)"
	case strings.HasPrefix(p, "/checks"):
		op.AccountingImp = "check status transitions post their own entries"
	case strings.HasPrefix(p, "/payroll"):
		op.AccountingImp = "payroll posting/payment entries"
	case strings.HasPrefix(p, "/company/fiscal-years"):
		op.AccountingImp = "closing entries / period control"
	}
	if op.Permission == "" && len(op.InnerPerms) == 0 && !strings.HasPrefix(p, "/auth") && !strings.HasPrefix(p, "/setup") {
		op.Notes = append(op.Notes, "no route-level permission (any signed-in user; the handler checks entity permissions where relevant)")
	}
}

func hasPrefixAny(s string, ps []string) bool {
	for _, p := range ps {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// ---------- frontend usage ----------

var callRe = regexp.MustCompile("(api\\.(get|post|put|del)|fetchPdf|downloadFile|useList)(<[^>()]*(\\([^)]*\\))?[^>()]*>)?\\(\\s*(['\"`][^'\"`]*['\"`]\\s*,\\s*)?['\"`]([^'\"`]+)['\"`]")
var tmplRe = regexp.MustCompile(`\$\{[^}]*\}`)

var rawRe = regexp.MustCompile("(?:fetch\\(\\s*[`'\"]/api/v1|path=\\{?\\s*[`'\"])(/[^`'\"\\s]+)[`'\"]")

// const base = `/${side}/${kind}` — a path kept in a variable and used with api.*
var varPathRe = regexp.MustCompile("(?:const|let)\\s+\\w+\\s*=\\s*[`'\"](/(?:\\$\\{[^}]+\\}|[a-z-]+)(?:/(?:\\$\\{[^}]+\\}|[a-z-]+))*)[`'\"]")

func uiCalls(repo string) []UICall {
	var out []UICall
	_ = filepath.Walk(filepath.Join(repo, "frontend", "src"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !(strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")) || strings.Contains(path, ".test.") {
			return nil
		}
		b, _ := os.ReadFile(path)
		rel, _ := filepath.Rel(filepath.Join(repo, "frontend", "src"), path)
		for _, m := range callRe.FindAllStringSubmatch(string(b), -1) {
			method := map[string]string{"api.get": "GET", "api.post": "POST", "api.put": "PUT", "api.del": "DELETE", "fetchPdf": "POST", "downloadFile": "GET", "useList": "GET"}[m[1]]
			p := m[6]
			if m[1] == "useList" && m[5] == "" {
				continue // useList(key, path): the first literal is the cache key
			}
			if strings.HasPrefix(p, "${") {
				// `${base}/${id}/post`: a path prefix held in a variable → wildcard for leading segments
				p = "{*}" + tmplRe.ReplaceAllString(strings.TrimPrefix(p, p[:strings.Index(p, "}")+1]), "{}")
			} else if !strings.HasPrefix(p, "/") {
				continue
			}
			p = strings.SplitN(tmplRe.ReplaceAllString(p, "{}"), "?", 2)[0]
			out = append(out, UICall{Method: method, Path: strings.TrimRight(p, "/"), File: rel})
		}
		// raw fetch('/api/v1/…') and <ServerExport path={`/…`}> style calls
		for _, m := range rawRe.FindAllStringSubmatch(string(b), -1) {
			p := strings.SplitN(tmplRe.ReplaceAllString(m[1], "{}"), "?", 2)[0]
			out = append(out, UICall{Method: "*", Path: strings.TrimRight(p, "/"), File: rel})
		}
		if strings.Contains(string(b), "api.") || strings.Contains(string(b), "useList") {
			for _, m := range varPathRe.FindAllStringSubmatch(string(b), -1) {
				p := strings.SplitN(tmplRe.ReplaceAllString(m[1], "{}"), "?", 2)[0]
				out = append(out, UICall{Method: "*", Path: strings.TrimRight(p, "/"), File: rel})
			}
		}
		return nil
	})
	return out
}

func pathMatch(pattern, actual string) bool {
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	if strings.HasPrefix(actual, "{*}") {
		rest := strings.Trim(strings.TrimPrefix(actual, "{*}"), "/")
		if rest == "" {
			return false // a bare variable path tells us nothing
		}
		rs := strings.Split(rest, "/")
		if len(ps) <= len(rs) {
			return false
		}
		return pathMatch(strings.Join(ps[len(ps)-len(rs):], "/"), rest) && len(ps)-len(rs) <= 2
	}
	as := strings.Split(strings.Trim(actual, "/"), "/")
	if len(ps) != len(as) {
		return false
	}
	for i := range ps {
		if strings.HasPrefix(ps[i], "{") || as[i] == "{}" || strings.Contains(as[i], "{}") {
			continue
		}
		if ps[i] != as[i] {
			return false
		}
	}
	return true
}

// ---------- main ----------

func main() {
	repo := flag.String("repo", "../..", "repository root")
	out := flag.String("out", "", "capabilities.json output")
	md := flag.String("md", "", "markdown matrix output")
	flag.Parse()
	internalDir = filepath.Join(*repo, "backend", "internal")
	srv := loadPkg("server")
	fd := srv.funcs["New"]
	if fd == nil {
		fmt.Fprintln(os.Stderr, "server.New not found")
		os.Exit(1)
	}
	root := &env{p: srv, vars: map[string]string{}, perms: map[string]string{}, pfuncs: map[string]string{}, recv: map[string]string{}, objs: map[string]objRef{}, module: "server"}
	root.walk("", fd.Body.List, "")
	var final []Operation
	seen := map[string]bool{}
	for _, op := range ops {
		if !strings.HasPrefix(op.Path, "/api/v1") {
			continue
		}
		op.Path = strings.TrimPrefix(op.Path, "/api/v1")
		if op.Path == "" {
			op.Path = "/"
		}
		op.Path = strings.TrimRight(op.Path, "/")
		if op.Path == "" {
			op.Path = "/"
		}
		key := op.Method + " " + op.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		seg := strings.Split(strings.Trim(op.Path, "/"), "/")[0]
		op.Module = seg
		classify(&op)
		op.ID = opID(op)
		final = append(final, op)
	}
	calls := uiCalls(*repo)
	for i := range final {
		files := map[string]bool{}
		for j, c := range calls {
			if (c.Method == final[i].Method || c.Method == "*") && pathMatch(final[i].Path, c.Path) {
				final[i].UIExists = true
				files[c.File] = true
				calls[j].Match = true
			}
		}
		for f := range files {
			final[i].UIEntryPoints = append(final[i].UIEntryPoints, f)
		}
		sort.Strings(final[i].UIEntryPoints)
	}
	var unmatched []UICall
	dedup := map[string]bool{}
	for _, c := range calls {
		k := c.Method + c.Path
		if !c.Match && !dedup[k] && c.Method != "*" {
			dedup[k] = true
			unmatched = append(unmatched, c)
		}
	}
	sort.Slice(final, func(i, j int) bool {
		if final[i].Module != final[j].Module {
			return final[i].Module < final[j].Module
		}
		return final[i].Path+final[i].Method < final[j].Path+final[j].Method
	})
	cat := Catalog{Generated: "tafakor-ai/service/cmd/capmap (from backend/internal + frontend/src source)", Operations: final, UnmatchedUI: unmatched}
	b, _ := json.MarshalIndent(cat, "", "  ")
	if *out != "" {
		_ = os.WriteFile(*out, b, 0o644)
	} else {
		os.Stdout.Write(b)
	}
	if *md != "" {
		_ = os.WriteFile(*md, []byte(markdown(cat)), 0o644)
	}
	fmt.Fprintf(os.Stderr, "capmap: %d operations, %d UI calls without backend\n", len(final), len(unmatched))
}

// opID: stable tool name, e.g. parties.create, treasury.transactions.cancel, sales.{kind}.post
func opID(op Operation) string {
	var segs []string
	for _, s := range strings.Split(strings.Trim(op.Path, "/"), "/") {
		if strings.HasPrefix(s, "{") && s != "{kind}" && s != "{report}" {
			continue
		}
		segs = append(segs, strings.Trim(s, "{}"))
	}
	base := strings.Join(segs, ".")
	if base == "" {
		base = "root"
	}
	last := op.Path[strings.LastIndex(op.Path, "/")+1:]
	verb := op.Kind
	switch op.Kind {
	case "read":
		if strings.HasPrefix(last, "{") && last != "{kind}" && last != "{report}" {
			verb = "get"
		} else {
			verb = "list"
		}
	case "workflow":
		// the verb is already the last path segment
		if a := op.Action; a != "" && !strings.HasSuffix(base, a) {
			base += "." + a
		}
		return strings.ReplaceAll(base, "-", "_")
	case "export", "compute":
		return strings.ReplaceAll(base, "-", "_")
	}
	return strings.ReplaceAll(base+"."+verb, "-", "_")
}

func markdown(c Catalog) string {
	var b strings.Builder
	b.WriteString("# Capability map (generated from source)\n\n")
	b.WriteString("Generated by `tafakor-ai/service/cmd/capmap` from `backend/internal` (routes, permissions, request structs, validators) and `frontend/src` (API calls). Do not edit by hand: `go run ./cmd/capmap -repo ../.. -out internal/catalog/capabilities.json -md ../CAPABILITIES.md`.\n\n")
	counts := map[string]int{}
	for _, o := range c.Operations {
		counts[o.Classification]++
	}
	b.WriteString(fmt.Sprintf("**%d operations.** ", len(c.Operations)))
	var cl []string
	for k, v := range counts {
		cl = append(cl, fmt.Sprintf("%s: %d", k, v))
	}
	sort.Strings(cl)
	b.WriteString(strings.Join(cl, " · ") + "\n\n")
	b.WriteString("Classification → AI behaviour: `READ_ONLY` runs without asking; `REQUIRES_USER_CONFIRMATION` shows an approval card (receipts, payments and expenses may run automatically within the user's limits); `REQUIRES_APPROVAL` always needs a human, never automatic; `NOT_SAFE_TO_AUTOMATE` is readable but never executed by the assistant; `NOT_SUPPORTED_BY_AI` is never called. Every call runs with the signed-in user's own permissions.\n\n")
	b.WriteString("| Module | Capability (tool) | Method & path | UI | Backend | AI tool | AI can execute | Confirmation | Permission | Inputs (required*) | Status |\n|---|---|---|:-:|:-:|:-:|---|---|---|---|---|\n")
	for _, o := range c.Operations {
		exec := map[string]string{"READ_ONLY": "READ", "REQUIRES_USER_CONFIRMATION": "YES", "REQUIRES_APPROVAL": "YES (human)", "NOT_SAFE_TO_AUTOMATE": "NO", "NOT_SUPPORTED_BY_AI": "NO", "FULLY_AUTONOMOUS": "YES"}[o.Classification]
		conf := map[string]string{"READ_ONLY": "no", "REQUIRES_USER_CONFIRMATION": "configurable", "REQUIRES_APPROVAL": "always", "NOT_SAFE_TO_AUTOMATE": "admin in app", "NOT_SUPPORTED_BY_AI": "—"}[o.Classification]
		status := map[string]string{"READ_ONLY": "Ready", "REQUIRES_USER_CONFIRMATION": "Ready", "REQUIRES_APPROVAL": "Ready (gated)", "NOT_SAFE_TO_AUTOMATE": "Restricted", "NOT_SUPPORTED_BY_AI": "Excluded"}[o.Classification]
		tool := "yes"
		if o.Classification == "NOT_SUPPORTED_BY_AI" || o.Multipart {
			tool = "no"
			if o.Multipart {
				status = "Upload via widget"
			}
		}
		var in []string
		for _, f := range o.Body {
			n := f.Name
			if f.Required {
				n += "*"
			}
			in = append(in, n)
		}
		if len(o.QueryParams) > 0 {
			in = append(in, "?"+strings.Join(o.QueryParams, ","))
		}
		perm := o.Permission
		if perm == "" && len(o.InnerPerms) > 0 {
			perm = "in handler: " + strings.Join(o.InnerPerms, ", ")
		}
		ui := "no"
		if o.UIExists {
			ui = "yes"
		}
		inputs := strings.Join(in, ", ")
		if len(inputs) > 160 {
			inputs = inputs[:157] + "…"
		}
		b.WriteString(fmt.Sprintf("| %s | `%s` | %s `%s` | %s | yes | %s | %s | %s | %s | %s | %s |\n", o.Module, o.ID, o.Method, o.Path, ui, tool, exec, conf, perm, inputs, status))
	}
	b.WriteString("\n## Backend operations without a UI call (Phase 12-D)\n\n")
	for _, o := range c.Operations {
		if !o.UIExists && o.Classification != "NOT_SUPPORTED_BY_AI" {
			b.WriteString(fmt.Sprintf("- `%s %s` (`%s`, %s)\n", o.Method, o.Path, o.ID, o.Classification))
		}
	}
	b.WriteString("\n## UI calls without a matching backend route (Phase 12-C, possibly broken)\n\n")
	if len(c.UnmatchedUI) == 0 {
		b.WriteString("None found.\n")
	}
	for _, u := range c.UnmatchedUI {
		b.WriteString(fmt.Sprintf("- `%s %s` in `%s`\n", u.Method, u.Path, u.File))
	}
	return b.String()
}

// enumValues collects string literals compared with == in a validation condition, or the keys of
// map[string]bool{"a": true, …}[x] lookups.
func enumValues(e ast.Expr) []string {
	var out []string
	ast.Inspect(e, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op == token.EQL {
				if s, ok := strLit(x.Y); ok && s != "" {
					out = append(out, s)
				} else if s, ok := strLit(x.X); ok && s != "" {
					out = append(out, s)
				}
			}
			if x.Op == token.NEQ || x.Op == token.LSS || x.Op == token.GTR {
				return false
			}
		case *ast.IndexExpr:
			if cl, ok := x.X.(*ast.CompositeLit); ok {
				if _, ok := cl.Type.(*ast.MapType); ok {
					for _, el := range cl.Elts {
						if kv, ok := el.(*ast.KeyValueExpr); ok {
							if s, ok := strLit(kv.Key); ok {
								out = append(out, s)
							}
						}
					}
				}
			}
		}
		return true
	})
	return out
}

func mergeEnum(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(a, b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
