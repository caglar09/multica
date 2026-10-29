package projectbundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectProjectContextReadsArbitraryTextAndExcludesUnsafePaths(t *testing.T) {
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("odd-layout/feature.logic", "feature evidence")
	write("config/nested/.env.local", "TOP_SECRET=value")
	write("node_modules/pkg/index.js", "generated dependency")
	write("binary.dat", "\x00\x01")
	if err := os.Symlink(filepath.Join(root, "odd-layout/feature.logic"), filepath.Join(root, "linked.logic")); err != nil {
		t.Fatal(err)
	}

	got, err := CollectProjectContext(root, CollectOptions{})
	if err != nil {
		t.Fatalf("CollectProjectContext() error = %v", err)
	}
	if got.RootName != filepath.Base(root) || len(got.Files) != 1 || got.Files[0].Path != "odd-layout/feature.logic" {
		t.Fatalf("collected context = %#v", got)
	}
	if strings.Contains(strings.Join(got.Warnings, " "), "secret") || len(got.Warnings) < 2 {
		t.Fatalf("warnings do not report excluded content: %#v", got.Warnings)
	}
}

func TestCollectProjectContextHonorsInputBudgets(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("12345"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := CollectProjectContext(root, CollectOptions{MaxFiles: 1, MaxFileBytes: 8, MaxTotalBytes: 8})
	if err != nil {
		t.Fatalf("CollectProjectContext() error = %v", err)
	}
	if len(got.Files) != 1 || !strings.Contains(strings.Join(got.Warnings, " "), "truncated") {
		t.Fatalf("budget result = %#v", got)
	}
}

func TestSanitizeProjectContextRejectsTraversalAndRemovesSensitiveFiles(t *testing.T) {
	if _, err := SanitizeProjectContext(ProjectContextPayload{Files: []ProjectContextFile{{Path: "../secret", Content: "value"}}}); err == nil {
		t.Fatal("SanitizeProjectContext accepted path traversal")
	}
	got, err := SanitizeProjectContext(ProjectContextPayload{RootName: "sample", Files: []ProjectContextFile{
		{Path: ".env", Content: "TOKEN=value"},
		{Path: "src/main.any", Content: "source"},
		{Path: "node_modules/pkg/index.js", Content: "dependency"},
	}})
	if err != nil {
		t.Fatalf("SanitizeProjectContext() error = %v", err)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "src/main.any" || len(got.Warnings) == 0 {
		t.Fatalf("sanitized context = %#v", got)
	}
}

func TestBuildBundleFromDiscoveryMapsProjectBrainSpecificationTasksAndDAG(t *testing.T) {
	report := ProjectDiscoveryReport{
		Project:             DiscoveredProject{Title: "Inventory", Purpose: "Track stock", Goal: "Reduce stock errors", ArchitectureSummary: "A small API and web client."},
		Boundaries:          DiscoveredBoundaries{InScope: []string{"Track quantities"}, OutOfScope: []string{"Shipping"}, Constraints: []string{"Keep the existing API"}, DefinitionOfDone: []string{"Quantity changes are auditable"}},
		ImplementedFeatures: []DiscoveredFeature{{Key: "feature-auth", Title: "Authentication", Description: "Users can sign in.", Evidence: []string{"src/auth.any"}}},
		NextTasks: []DiscoveredTask{
			{Key: "task-audit", Title: "Add quantity audit history", Description: "Persist changes.", Priority: "high", Stage: 1, BlockedByKeys: []string{"feature-auth"}, AcceptanceCriteria: []string{"Changes have an actor and time."}},
			{Key: "task-report", Title: "Add stock report", Description: "Summarize inventory.", Priority: "medium", Stage: 2, ParentKey: "task-audit", BlockedByKeys: []string{"task-audit"}},
		},
	}
	context := &ProjectContextPayload{Warnings: []string{"Large files were excluded."}}
	bundle, err := BuildBundleFromDiscovery(report, context)
	if err != nil {
		t.Fatalf("BuildBundleFromDiscovery() error = %v", err)
	}
	if bundle.Project.Title != report.Project.Title || bundle.Project.Description == nil || *bundle.Project.Description != report.Project.Purpose {
		t.Fatalf("project mapping = %#v", bundle.Project)
	}
	if bundle.Bootstrap == nil || bundle.Bootstrap.Plan == nil || bundle.Bootstrap.Plan.Specification == nil {
		t.Fatal("bundle is missing bootstrap plan specification")
	}
	if got := bundle.Bootstrap.Plan.Specification.NonGoals; len(got) != 1 || got[0] != "Shipping" {
		t.Fatalf("non-goals = %#v", got)
	}
	if len(bundle.Bootstrap.Knowledge) < 5 || len(bundle.Resources) != 0 {
		t.Fatalf("brain/resources mapping = %d knowledge entries, %d resources", len(bundle.Bootstrap.Knowledge), len(bundle.Resources))
	}
	if len(bundle.Tasks) != 3 || bundle.Tasks[0].Status == nil || *bundle.Tasks[0].Status != "done" || bundle.Tasks[0].Stage != nil {
		t.Fatalf("implemented feature mapping = %#v", bundle.Tasks)
	}
	if bundle.Tasks[1].Status == nil || *bundle.Tasks[1].Status != "todo" || len(bundle.Tasks[1].BlockedByKeys) != 1 || bundle.Tasks[1].BlockedByKeys[0] != "feature-auth" {
		t.Fatalf("next-task mapping = %#v", bundle.Tasks[1])
	}
	if len(bundle.Bootstrap.Plan.Edges) != 2 || bundle.Bootstrap.Plan.Edges[0].From != "feature-auth" || bundle.Bootstrap.Plan.Edges[0].To != "task-audit" {
		t.Fatalf("plan edges = %#v", bundle.Bootstrap.Plan.Edges)
	}
	if err := ValidateBundleForImport(bundle); err != nil {
		t.Fatalf("ValidateBundleForImport() error = %v", err)
	}
}

func TestValidateDiscoveryReportRejectsUnknownDependencies(t *testing.T) {
	report := ProjectDiscoveryReport{
		Project:   DiscoveredProject{Title: "Project", Purpose: "Purpose", Goal: "Goal", ArchitectureSummary: "Architecture"},
		NextTasks: []DiscoveredTask{{Key: "task", Title: "Task", Stage: 1, BlockedByKeys: []string{"missing"}}},
	}
	if err := ValidateDiscoveryReport(report); err == nil || !strings.Contains(err.Error(), "dependency") {
		t.Fatalf("ValidateDiscoveryReport() error = %v, want invalid dependency", err)
	}
}
