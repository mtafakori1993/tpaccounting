package main

import (
	"go/ast"
	"regexp"
	"sort"
	"strings"
)

// Posting analysis: does an operation create or change accounting entries?
//
// Answered from the code, not from the URL: the handler's call graph is followed
// (same-package functions and methods, and calls into other internal packages)
// until it reaches the ledger's write API. The conditions guarding that call are
// kept: "if in.OpeningBalance.IsPositive() { … ledger.PostAuto(…) }" means the
// operation posts only when opening_balance is set, "if in.Post { Post(…) }" only
// when the caller asks to post. An operation reached unconditionally always posts.

// ledgerWrites are the ledger package's functions that write the books.
var ledgerWrites = map[string]bool{"PostAuto": true, "Post": true, "Cancel": true, "Create": true, "Update": true}

type postResult struct {
	reaches       bool
	unconditional bool
	triggers      map[string]bool // Go field names that guard every posting path
}

type fnKey struct {
	pkg string
	fn  *ast.FuncDecl
}

var postMemo = map[fnKey]*postResult{}

// postingOf analyses a handler.
func postingOf(p *pkg, fd *ast.FuncDecl) (posts bool, always bool, triggers []string) {
	if fd == nil || fd.Body == nil {
		return false, false, nil
	}
	r := analyse(p, fd, 0)
	for t := range r.triggers {
		triggers = append(triggers, snake(t))
	}
	sort.Strings(triggers)
	return r.reaches, r.reaches && r.unconditional, triggers
}

func analyse(p *pkg, fd *ast.FuncDecl, depth int) *postResult {
	k := fnKey{p.name, fd}
	if r, ok := postMemo[k]; ok {
		return r
	}
	res := &postResult{triggers: map[string]bool{}}
	if depth > 25 || fd.Body == nil {
		return res // too deep to follow: not memoized, so a shallower path can still find it
	}
	postMemo[k] = res // a cycle back into this function resolves to "not reached"
	var walk func(n ast.Node, conds []ast.Expr)
	hit := func(conds []ast.Expr, callee *postResult) {
		res.reaches = true
		var mine []string
		for _, c := range conds {
			mine = append(mine, inputFields(c)...)
		}
		switch {
		case len(mine) == 0 && callee.unconditional:
			res.unconditional = true
		default:
			for _, f := range mine {
				res.triggers[f] = true
			}
			for f := range callee.triggers {
				res.triggers[f] = true
			}
		}
	}
	walk = func(n ast.Node, conds []ast.Expr) {
		ast.Inspect(n, func(x ast.Node) bool {
			switch s := x.(type) {
			case *ast.IfStmt:
				if s.Init != nil {
					walk(s.Init, conds)
				}
				walk(s.Cond, conds)
				walk(s.Body, append(append([]ast.Expr{}, conds...), s.Cond))
				if s.Else != nil {
					walk(s.Else, append(append([]ast.Expr{}, conds...), s.Cond))
				}
				return false
			case *ast.CallExpr:
				if r := callTarget(p, s, depth); r != nil && r.reaches {
					hit(conds, r)
				}
				// a function passed by name is called through the parameter
				// (h.action(w, r, "post", Post) → Post)
				for _, a := range s.Args {
					switch a.(type) {
					case *ast.Ident, *ast.SelectorExpr:
						if r := callTarget(p, &ast.CallExpr{Fun: a}, depth); r != nil && r.reaches {
							hit(conds, r)
						}
					}
				}
			}
			return true
		})
	}
	walk(fd.Body, nil)
	if res.unconditional {
		res.triggers = map[string]bool{}
	}
	return res
}

// callTarget resolves a call to the posting result of its target.
func callTarget(p *pkg, c *ast.CallExpr, depth int) *postResult {
	switch f := c.Fun.(type) {
	case *ast.Ident:
		if p.name == "ledger" && ledgerWrites[f.Name] { // the journal handlers live in the ledger package
			return &postResult{reaches: true, unconditional: true, triggers: map[string]bool{}}
		}
		if fd := p.funcs[f.Name]; fd != nil {
			return analyse(p, fd, depth+1)
		}
	case *ast.SelectorExpr:
		if id, ok := f.X.(*ast.Ident); ok {
			if dir, ok := p.imports[id.Name]; ok {
				if dir == "ledger" && ledgerWrites[f.Sel.Name] {
					return &postResult{reaches: true, unconditional: true, triggers: map[string]bool{}}
				}
				q := loadPkg(dir)
				if fd := q.funcs[f.Sel.Name]; fd != nil {
					return analyse(q, fd, depth+1)
				}
				return nil
			}
		}
		// a method of this package (h.svc.Create, s.post …): any receiver with that name
		var merged *postResult
		for _, key := range sortedKeys(p.funcs) {
			fd := p.funcs[key]
			if strings.HasSuffix(key, "."+f.Sel.Name) {
				if r := analyse(p, fd, depth+1); r.reaches {
					if merged == nil || (r.unconditional && !merged.unconditional) {
						merged = r
					}
				}
			}
		}
		return merged
	}
	return nil
}

// inputFields: exported fields read from a variable in a condition ("in.Post",
// "in.OpeningBalance.IsPositive()"), i.e. the request values the branch depends on.
func inputFields(e ast.Expr) []string {
	var out []string
	ast.Inspect(e, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := s.X.(*ast.Ident); ok && ast.IsExported(s.Sel.Name) && !ast.IsExported(id.Name) {
				out = append(out, s.Sel.Name)
			}
		}
		return true
	})
	return out
}

var camelRe = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// snake converts a Go field name to the JSON name convention of the app (OpeningBalance → opening_balance).
func snake(s string) string {
	s = strings.ReplaceAll(s, "ID", "Id")
	return strings.ToLower(camelRe.ReplaceAllString(s, "${1}_${2}"))
}
