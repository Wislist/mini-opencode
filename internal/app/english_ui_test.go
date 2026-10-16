package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func TestRuntimeStringsUseEnglish(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	dirs := []string{"internal/agent", "internal/app", "internal/config", "internal/tui"}

	for _, dir := range dirs {
		root := filepath.Join(repoRoot, dir)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			fileset := token.NewFileSet()
			file, err := parser.ParseFile(fileset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil {
					return true
				}
				if strings.ContainsFunc(value, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
					t.Errorf("non-English runtime string at %s: %q", fileset.Position(literal.Pos()), value)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", dir, err)
		}
	}
}
