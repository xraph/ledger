package ledger

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/xraph/ledger/types"
)

// Every sentinel in errors.go has a name errorType can give a span. A new
// sentinel left out of the table would show up as *errors.errorString.
func TestErrorTypeNamesEverySentinel(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "errors.go", nil, 0)
	if err != nil {
		t.Fatalf("parse errors.go: %v", err)
	}
	named := map[string]error{}
	for _, s := range sentinels {
		named[s.name] = s.err
	}
	found := 0
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			for _, ident := range spec.(*ast.ValueSpec).Names {
				if !strings.HasPrefix(ident.Name, "Err") {
					continue
				}
				found++
				sentinel, ok := named["ledger."+ident.Name]
				if !ok {
					t.Errorf("%s is missing from the sentinels table", ident.Name)
					continue
				}
				if got := errorType(fmt.Errorf("wrapped: %w", sentinel)); got != "ledger."+ident.Name {
					t.Errorf("errorType of a wrapped %s = %q", ident.Name, got)
				}
			}
		}
	}
	if found == 0 || found+1 != len(sentinels) { // +1: types.ErrOverflow
		t.Errorf("errors.go declares %d sentinels, the table names %d", found, len(sentinels))
	}
}

type driverError struct{ msg string }

func (e *driverError) Error() string { return e.msg }

func TestErrorTypeNeverReturnsTheMessage(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&driverError{"duplicate key alice@example.com"}, "*ledger.driverError"},
		{fmt.Errorf("insert: %w", &driverError{"alice@example.com"}), "*ledger.driverError"},
		{errors.Join(fmt.Errorf("row 1: %w", &driverError{"x"}), errors.New("row 2")), "*ledger.driverError"},
		{fmt.Errorf("subtotal: %w", types.ErrOverflow), "types.ErrOverflow"},
		{fmt.Errorf("run: %w", context.DeadlineExceeded), "context.DeadlineExceeded"},
		{errors.Join(ErrInvalidInput, context.Canceled), "context.Canceled"},
		{errors.New("alice@example.com"), "*errors.errorString"},
	}
	for _, c := range cases {
		if got := errorType(c.err); got != c.want {
			t.Errorf("errorType(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}
