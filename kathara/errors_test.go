package kathara

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

func TestAliasesAreTheSameObjects(t *testing.T) {
	t.Parallel()

	pairs := []struct {
		name  string
		local error
		kerr  error
	}{
		{"ErrLabNotFound", ErrLabNotFound, kerrors.ErrLabNotFound},
		{"ErrMachineNotFound", ErrMachineNotFound, kerrors.ErrMachineNotFound},
		{"ErrMachineNotRunning", ErrMachineNotRunning, kerrors.ErrMachineNotRunning},
		{"ErrMachineAlreadyExists", ErrMachineAlreadyExists, kerrors.ErrMachineAlreadyExists},
		{"ErrLinkNotFound", ErrLinkNotFound, kerrors.ErrLinkNotFound},
		{"ErrDaemonConnection", ErrDaemonConnection, kerrors.ErrDaemonConnection},
		{"ErrDependencyLoop", ErrDependencyLoop, kerrors.ErrDependencyLoop},
		{"ErrInvocation", ErrInvocation, kerrors.ErrInvocation},
		{"ErrMachineBinary", ErrMachineBinary, kerrors.ErrMachineBinary},
		{"ErrMachineCollisionDomain", ErrMachineCollisionDomain, kerrors.ErrMachineCollisionDomain},
		{"ErrPrivilege", ErrPrivilege, kerrors.ErrPrivilege},
		{"ErrSettings", ErrSettings, kerrors.ErrSettings},
	}

	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()

			if p.local != p.kerr {
				t.Fatalf("%s is not the kerrors value", p.name)
			}
			if !errors.Is(p.kerr, p.local) || !errors.Is(p.local, p.kerr) {
				t.Errorf("%s does not match in both directions", p.name)
			}
		})
	}

	err := kerrors.NewMachineNotRunning("pc1")
	if !errors.Is(err, ErrMachineNotRunning) {
		t.Error("a kerrors-built error does not match the kathara alias")
	}

	// The struct types are aliases too, so errors.As reaches through.
	var machineErr *MachineError
	wrapped := kerrors.WrapMachine("pc1", "deploy", err)
	if !errors.As(wrapped, &machineErr) {
		t.Fatalf("errors.As(%v, *kathara.MachineError) failed", wrapped)
	}
	if machineErr.Machine != "pc1" || machineErr.Op != "deploy" {
		t.Errorf("MachineError = %+v, want pc1/deploy", machineErr)
	}
}

// TestAliasedFunctionsForward keeps the three inspection helpers wired to
// `kerrors` rather than reimplemented.
func TestAliasedFunctionsForward(t *testing.T) {
	t.Parallel()

	err := kerrors.NewMachineNotRunning("pc1")
	if got, want := Code(err), kerrors.Code(err); got != want {
		t.Errorf("Code = %q, want %q", got, want)
	}
	if got, want := Code(err), CodeMachineNotRunning; got != want {
		t.Errorf("Code = %q, want %q", got, want)
	}
	if got, want := HumanLabel(CodeMachineNotRunning), "MachineNotRunningError"; got != want {
		t.Errorf("HumanLabel = %q, want %q", got, want)
	}

	if got, want := Code(errBackend), CodeInternalError; got != want {
		t.Errorf("Code(untyped) = %q, want %q", got, want)
	}
	if got := Code(nil); got != "" {
		t.Errorf("Code(nil) = %q, want empty", got)
	}

	joined := errors.Join(err, kerrors.NewLinkNotFoundQuoted("A"))
	if got, want := len(Joined(joined)), len(kerrors.Joined(joined)); got != want {
		t.Errorf("Joined returned %d branches, kerrors says %d", got, want)
	}

	if !slices.Equal(AllCodes, kerrors.AllCodes) {
		t.Error("AllCodes is not the kerrors table")
	}
}

// TestEveryInspectableKerrorsSymbolIsAliased is D-1's promise made checkable:
// "the surface holds".
func TestEveryInspectableKerrorsSymbolIsAliased(t *testing.T) {
	t.Parallel()

	taxonomy := parseExports(t, "../kerrors")
	aliased := parseExports(t, ".")

	for _, kind := range []token.Token{token.CONST, token.VAR, token.TYPE} {
		for _, name := range taxonomy[kind] {
			if !slices.Contains(aliased[kind], name) {
				t.Errorf("kerrors exports %s %s with no alias in kathara/errors.go", strings.ToLower(kind.String()), name)
			}
		}
	}
}

// parseExports returns the exported constant, variable and type names declared
// in the package rooted at dir, keyed by the token that declared them and
// ignoring test files.
func parseExports(t *testing.T, dir string) map[token.Token][]string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	exports := map[token.Token][]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					for _, ident := range s.Names {
						if ident.IsExported() {
							exports[gen.Tok] = append(exports[gen.Tok], ident.Name)
						}
					}
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						exports[token.TYPE] = append(exports[token.TYPE], s.Name.Name)
					}
				}
			}
		}
	}
	return exports
}
