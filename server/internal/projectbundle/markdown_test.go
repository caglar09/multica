package projectbundle

import (
	"strings"
	"testing"
)

func TestMarkdownRenderParseAndReconcile(t *testing.T) {
	done, todo := "done", "in_progress"
	categoryDone, categoryStarted := "done", "started"
	parent := "MUL-1"
	bundle := Bundle{
		SchemaVersion: SchemaVersionV1,
		Project:       PortableProject{Title: "Portable project"},
		Tasks: []PortableTask{
			{Key: "MUL-1", OriginID: "source-1", Title: "Parent", Status: &done, StatusCategory: &categoryDone, Position: float64ptr(0)},
			{Key: "MUL-2", Title: "Child", ParentKey: &parent, Status: &todo, StatusCategory: &categoryStarted, Position: float64ptr(0)},
		},
	}
	markdown := RenderTasksMarkdown(bundle)
	if !strings.Contains(markdown, "- [x] Parent <!-- multica:key=MUL-1 origin_id=source-1 -->") {
		t.Fatalf("render omitted portable identity/status: %s", markdown)
	}
	updated, err := ReconcileBundleWithMarkdown(bundle, strings.Replace(markdown, "- [x] Parent", "- [ ] Parent", 1)+"\n- [x] New task\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Tasks) != 3 {
		t.Fatalf("reconciled %d tasks, want 3", len(updated.Tasks))
	}
	if updated.Tasks[0].Status == nil || *updated.Tasks[0].Status != "todo" {
		t.Fatalf("unchecked existing task status = %v, want todo", updated.Tasks[0].Status)
	}
	if updated.Tasks[1].Status == nil || *updated.Tasks[1].Status != "in_progress" {
		t.Fatalf("unchanged checkbox overwrote status with %v", updated.Tasks[1].Status)
	}
	if updated.Tasks[2].ParentKey != nil || updated.Tasks[2].Status == nil || *updated.Tasks[2].Status != "done" {
		t.Fatalf("new task was not parsed as a top-level completed task: %#v", updated.Tasks[2])
	}
	preview := BuildPreview(bundle, updated, false)
	if preview.ProjectAction != "unchanged" || len(preview.ProjectChanges) != 0 {
		t.Fatalf("Markdown reconciliation changed project fields: %+v", preview)
	}
}

func TestMarkdownParserBuildsNestedTasksAndTitle(t *testing.T) {
	bundle, err := ParseMarkdownToBundle("hint", "# Imported\n\n- [ ] Parent\n  - [x] Child\n")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Project.Title != "Imported" || len(bundle.Tasks) != 2 {
		t.Fatalf("unexpected parsed bundle: %#v", bundle)
	}
	if bundle.Tasks[1].ParentKey == nil || *bundle.Tasks[1].ParentKey != bundle.Tasks[0].Key {
		t.Fatalf("nested task parent = %v, want %q", bundle.Tasks[1].ParentKey, bundle.Tasks[0].Key)
	}
}

func float64ptr(value float64) *float64 { return &value }
