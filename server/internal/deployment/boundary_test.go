package deployment

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestZeroDependencyBoundary(t *testing.T) {
	t.Parallel()

	forbiddenPrefixes := []string{
		"github.com/multica-ai/multica/server/internal/daemon",
		"github.com/multica-ai/multica/server/internal/scheduler",
		"github.com/multica-ai/multica/server/internal/dispatch",
		"github.com/multica-ai/multica/server/internal/database",
		"github.com/multica-ai/multica/server/internal/dbreader",
		"github.com/multica-ai/multica/server/internal/service",
		"github.com/multica-ai/multica/server/internal/handler",
		"github.com/multica-ai/multica/server/internal/agentconfig",
		"github.com/multica-ai/multica/server/internal/projectorchestration",
		"github.com/multica-ai/multica/server/internal/workflowruntime",
	}

	dirsToCheck := []string{
		".",
		"../integrations/deployment",
	}

	for _, dir := range dirsToCheck {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("Failed to read directory %s: %v", dir, err)
		}

		fset := token.NewFileSet()
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}

			filePath := filepath.Join(dir, entry.Name())
			fileAst, err := parser.ParseFile(fset, filePath, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("Failed to parse imports for %s: %v", filePath, err)
			}

			for _, imp := range fileAst.Imports {
				importPath := strings.Trim(imp.Path.Value, `"`)
				for _, forbidden := range forbiddenPrefixes {
					if strings.HasPrefix(importPath, forbidden) {
						t.Errorf("Architecture boundary violation in %s: imports forbidden package %q", filePath, importPath)
					}
				}
			}
		}
	}
}
