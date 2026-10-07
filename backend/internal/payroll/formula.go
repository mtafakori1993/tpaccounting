package payroll

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/shopspring/decimal"
)

// Formula components compute their amount from an arithmetic expression over
// named payroll variables, e.g. "base_salary * 0.05 + children * rate" or
// "min(overtime_hours, 40) * hourly_wage * 1.4". Supported: numbers,
// variables, + − × ÷, parentheses, unary minus and min/max/abs. Arithmetic is
// exact decimal; division keeps 16 fractional digits before final rounding.

// FormulaVariables documents the variables a formula may use.
var FormulaVariables = []string{
	"base_salary", "daily_wage", "hourly_wage", "period_days", "monthly_hours", "working_days", "working_hours", "absence_days",
	"unpaid_leave_days", "paid_leave_days", "overtime_hours", "mission_days", "holiday_work_hours", "children", "rate", "manual",
	"gross", "taxable", "insurable",
}

type formulaParser struct {
	src  []rune
	pos  int
	vars map[string]decimal.Decimal
	// check only validates syntax and names; it never divides.
	check bool
}

// FormulaError describes an invalid formula.
type FormulaError struct {
	Pos int
	Msg string
}

func (e *FormulaError) Error() string { return fmt.Sprintf("formula: %s at %d", e.Msg, e.Pos) }

func (p *formulaParser) fail(msg string) { panic(&FormulaError{Pos: p.pos, Msg: msg}) }

func (p *formulaParser) skip() {
	for p.pos < len(p.src) && unicode.IsSpace(p.src[p.pos]) {
		p.pos++
	}
}

func (p *formulaParser) peek() rune {
	p.skip()
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *formulaParser) expr() decimal.Decimal {
	v := p.term()
	for {
		switch p.peek() {
		case '+':
			p.pos++
			v = v.Add(p.term())
		case '-':
			p.pos++
			v = v.Sub(p.term())
		default:
			return v
		}
	}
}

func (p *formulaParser) term() decimal.Decimal {
	v := p.unary()
	for {
		switch p.peek() {
		case '*', '×':
			p.pos++
			v = v.Mul(p.unary())
		case '/', '÷':
			p.pos++
			d := p.unary()
			if p.check {
				continue
			}
			if d.IsZero() {
				v = decimal.Zero // division by zero yields 0 (e.g. no working days)
				continue
			}
			v = v.DivRound(d, 16)
		default:
			return v
		}
	}
}

func (p *formulaParser) unary() decimal.Decimal {
	if p.peek() == '-' {
		p.pos++
		return p.unary().Neg()
	}
	if p.peek() == '+' {
		p.pos++
		return p.unary()
	}
	return p.primary()
}

func (p *formulaParser) primary() decimal.Decimal {
	c := p.peek()
	switch {
	case c == '(':
		p.pos++
		v := p.expr()
		if p.peek() != ')' {
			p.fail("missing )")
		}
		p.pos++
		return v
	case unicode.IsDigit(c) || c == '.':
		start := p.pos
		for p.pos < len(p.src) && (unicode.IsDigit(p.src[p.pos]) || p.src[p.pos] == '.') {
			p.pos++
		}
		d, err := decimal.NewFromString(asciiDigits(string(p.src[start:p.pos])))
		if err != nil {
			p.fail("invalid number")
		}
		return d
	case unicode.IsLetter(c) || c == '_':
		start := p.pos
		for p.pos < len(p.src) && (unicode.IsLetter(p.src[p.pos]) || unicode.IsDigit(p.src[p.pos]) || p.src[p.pos] == '_') {
			p.pos++
		}
		name := strings.ToLower(string(p.src[start:p.pos]))
		if p.peek() == '(' {
			p.pos++
			var args []decimal.Decimal
			if p.peek() != ')' {
				for {
					args = append(args, p.expr())
					if p.peek() == ',' {
						p.pos++
						continue
					}
					break
				}
			}
			if p.peek() != ')' {
				p.fail("missing )")
			}
			p.pos++
			switch name {
			case "min", "max":
				if len(args) < 2 {
					p.fail(name + " needs at least two arguments")
				}
				v := args[0]
				for _, a := range args[1:] {
					if name == "min" {
						v = decimal.Min(v, a)
					} else {
						v = decimal.Max(v, a)
					}
				}
				return v
			case "abs":
				if len(args) != 1 {
					p.fail("abs needs one argument")
				}
				return args[0].Abs()
			}
			p.fail("unknown function " + name)
		}
		v, ok := p.vars[name]
		if !ok {
			p.fail("unknown variable " + name)
		}
		return v
	}
	if c == 0 {
		p.fail("unexpected end")
	}
	p.fail(fmt.Sprintf("unexpected %q", c))
	return decimal.Zero
}

func runFormula(src string, vars map[string]decimal.Decimal, check bool) (v decimal.Decimal, err error) {
	if len(src) > 1000 {
		return decimal.Zero, &FormulaError{Msg: "too long"}
	}
	p := &formulaParser{src: []rune(src), vars: vars, check: check}
	defer func() {
		if r := recover(); r != nil {
			fe, ok := r.(*FormulaError)
			if !ok {
				panic(r)
			}
			err = fe
		}
	}()
	if strings.TrimSpace(src) == "" {
		return decimal.Zero, &FormulaError{Msg: "empty"}
	}
	v = p.expr()
	if p.peek() != 0 {
		p.fail("unexpected input")
	}
	return v, nil
}

// EvalFormula evaluates a formula with the given variables.
func EvalFormula(src string, vars map[string]decimal.Decimal) (decimal.Decimal, error) {
	return runFormula(src, vars, false)
}

// CheckFormula validates syntax and variable names.
func CheckFormula(src string) error {
	vars := map[string]decimal.Decimal{}
	for _, n := range FormulaVariables {
		vars[n] = decimal.NewFromInt(1)
	}
	_, err := runFormula(src, vars, true)
	return err
}
