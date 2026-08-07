package kubernetes

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestClientGoIsConfinedToThisPackage is PORT_SPEC §0.2 #8 and
// PACKAGE_GRAPH.md §1.2: a Docker-only build must be able to leave `client-go`
// out of the binary, and the only mechanism for that is that NOTHING outside
// this package names it.
//
// That is why there is no `init()` here and why [Backend] is a function
// `cmd/kathara` calls from `backends_all.go`: an import-time registration would
// force every build that links the CLI to link `client-go` too.
//
// The check is a source scan rather than a `go list -deps` because it has to
// hold for a build that does not exist yet — `cmd/kathara` is a later stage —
// and because a test that shells out to the toolchain is not one that runs
// anywhere.
func TestClientGoIsConfinedToThisPackage(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("module root not found at %s: %v", root, err)
	}

	// The packages that ARE allowed to name client-go, as module-relative
	// directories.
	allowed := map[string]bool{
		filepath.Join("backend", "kubernetes"): true,
	}

	// Prefixes that are not Go source of this module.
	skipDirs := map[string]bool{
		".git": true, "docs": true, "python": true, "test": true, "tools": true, "testdata": true,
	}

	fileSet := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if entry.IsDir() {
			if skipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if allowed[filepath.Dir(relative)] {
			return nil
		}

		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imported := range file.Imports {
			value, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			if strings.HasPrefix(value, "k8s.io/") {
				t.Errorf("%s imports %s — client-go must stay inside backend/kubernetes", relative, value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// TestNoInitFunction pins the other half of the same rule: registration is a
// call `cmd/kathara` makes, not something that happens because the package was
// linked. An `init()` here would also make the registration ORDER depend on the
// import graph, which PACKAGE_GRAPH.md §1.2 rules out.
func TestNoInitFunction(t *testing.T) {
	fileSet := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if function.Recv == nil && function.Name.Name == "init" {
				t.Errorf("%s declares init(): registration must stay explicit", name)
			}
		}
	}
}
