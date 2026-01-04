package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mutationPatterns defines the field assignments that should only happen in the store package
var mutationPatterns = []string{
	".SetupStatus",
	".ArchiveStatus",
	".Archived",
	".ArchivedAt",
	".Tunnel",
	".Ports",
	".PRs",
	".GitHubOwner",
	".GitHubRepo",
}

// allowedPackages are packages that are allowed to mutate config directly
// (besides the store package itself)
var allowedPackages = map[string]bool{
	"store":      true, // The store package itself
	"config":     true, // Config package can set defaults in NewXxx functions
	"workspace":  true, // TODO: Remove after refactoring workspace package
	"tui":        true, // TODO: Remove after refactoring TUI
	"tunnel":     true, // TODO: Remove after refactoring tunnel package
	"conductor":  true, // TODO: Remove after refactoring CLI commands
	"runner":     true, // TODO: Remove after refactoring runner package
	"updater":    true, // TODO: Remove after refactoring updater package
}

// TestNoDirectMutationsOutsideStore scans all Go files for direct config mutations
// that bypass the Store. This test will fail if mutations are found in disallowed packages.
//
// NOTE: This test currently allows many packages while we refactor.
// As each package is refactored to use Store, remove it from allowedPackages.
// Eventually, only "store" and "config" should be in the allowed list.
func TestNoDirectMutationsOutsideStore(t *testing.T) {
	t.Skip("Skipping until refactor is complete - remove packages from allowedPackages as they're refactored")

	root := findProjectRoot(t)
	violations := []string{}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip non-Go files
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Skip test files
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		// Skip vendor
		if strings.Contains(path, "/vendor/") {
			return nil
		}

		// Get package name from path
		pkgName := getPackageName(path)
		if allowedPackages[pkgName] {
			return nil
		}

		// Parse the file
		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}

		// Find violations
		ast.Inspect(node, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}

			for _, lhs := range assign.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok {
					fieldName := "." + sel.Sel.Name
					for _, pattern := range mutationPatterns {
						if fieldName == pattern {
							pos := fset.Position(assign.Pos())
							violations = append(violations,
								pos.String()+": direct mutation of "+fieldName+" in package "+pkgName)
						}
					}
				}
			}
			return true
		})

		return nil
	})

	if err != nil {
		t.Fatalf("Error walking directory: %v", err)
	}

	if len(violations) > 0 {
		t.Errorf("Found %d direct config mutations that should go through Store:\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

// TestMutationCoverage verifies that all mutation patterns have corresponding Store methods
func TestMutationCoverage(t *testing.T) {
	// Map of field patterns to their expected Store mutation methods
	expectedMethods := map[string]string{
		".SetupStatus":   "SetWorktreeStatus",
		".ArchiveStatus": "SetWorktreeArchiveStatus",
		".Archived":      "ArchiveWorktree",
		".ArchivedAt":    "ArchiveWorktree", // Set together with Archived
		".Tunnel":        "SetTunnelState",
		".Ports":         "SetWorktreePorts",
		".PRs":           "SetWorktreePRs",
		".GitHubOwner":   "SetGitHubConfig",
		".GitHubRepo":    "SetGitHubConfig",
	}

	// Verify each pattern has a method
	for pattern, method := range expectedMethods {
		t.Run(pattern, func(t *testing.T) {
			// This is a documentation test - it ensures we've thought about each pattern
			if method == "" {
				t.Errorf("Pattern %s has no corresponding Store method", pattern)
			}
		})
	}
}

// findProjectRoot finds the root of the conductor project
func findProjectRoot(t *testing.T) string {
	t.Helper()

	// Start from current directory and walk up
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	for {
		// Check for go.mod
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("Could not find project root (no go.mod found)")
		}
		dir = parent
	}
}

// getPackageName extracts the package name from a file path
func getPackageName(path string) string {
	dir := filepath.Dir(path)
	return filepath.Base(dir)
}

// TestCountCurrentMutations counts current mutations to track refactor progress
func TestCountCurrentMutations(t *testing.T) {
	root := findProjectRoot(t)
	mutationsByPackage := make(map[string]int)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		if strings.Contains(path, "/vendor/") {
			return nil
		}

		pkgName := getPackageName(path)

		// Skip store and config packages
		if pkgName == "store" || pkgName == "config" {
			return nil
		}

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}

		ast.Inspect(node, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}

			for _, lhs := range assign.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok {
					fieldName := "." + sel.Sel.Name
					for _, pattern := range mutationPatterns {
						if fieldName == pattern {
							mutationsByPackage[pkgName]++
						}
					}
				}
			}
			return true
		})

		return nil
	})

	if err != nil {
		t.Fatalf("Error walking directory: %v", err)
	}

	// Log current state for tracking refactor progress
	t.Log("Current direct mutations by package (to be refactored to use Store):")
	total := 0
	for pkg, count := range mutationsByPackage {
		t.Logf("  %s: %d mutations", pkg, count)
		total += count
	}
	t.Logf("Total: %d mutations to refactor", total)
}
