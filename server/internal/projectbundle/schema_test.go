package projectbundle

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeBundleAcceptsSparseSyncAndRejectsIncompleteImport(t *testing.T) {
	bundle, err := DecodeBundle([]byte(`{"schema_version":"multica.project.v1","project":{},"tasks":[{"key":"TASK-1","status":"done"}]}`))
	if err != nil {
		t.Fatalf("DecodeBundle sparse sync: %v", err)
	}
	if bundle.Tasks[0].Has("title") {
		t.Fatal("omitted task title was marked as supplied")
	}
	if err := ValidateBundleForImport(bundle); err == nil {
		t.Fatal("ValidateBundleForImport accepted a project/task without titles")
	}
}

func TestDecodeBundleTracksAttachmentsButDoesNotSerializeThem(t *testing.T) {
	bundle, err := DecodeBundle([]byte(`{
		"schema_version":"multica.project.v1",
		"project":{"title":"portable"},
		"tasks":[{"key":"TASK-1","title":"Task","attachments":[{"name":"task.txt","data":"secret"}],"comments":[{"key":"comment-1","content":"note","attachments":[{"data":"secret"}]}]}]
	}`))
	if err != nil {
		t.Fatalf("DecodeBundle attachments: %v", err)
	}
	if bundle.Tasks[0].attachmentCount != 1 || bundle.Tasks[0].Comments[0].attachmentCount != 1 {
		t.Fatalf("attachment counts = task %d, comment %d; want 1 each", bundle.Tasks[0].attachmentCount, bundle.Tasks[0].Comments[0].attachmentCount)
	}
	if !bundle.HasUnsupportedAttachments() {
		t.Fatal("bundle did not report unsupported attachments")
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal portable bundle: %v", err)
	}
	if strings.Contains(string(encoded), "attachments") || strings.Contains(string(encoded), "secret") {
		t.Fatalf("bundle response serialized unsupported attachment data: %s", encoded)
	}
}

func TestBundleMarshalPreservesSparseAndExplicitEmptyFields(t *testing.T) {
	bundle, err := DecodeBundle([]byte(`{
		"schema_version":"multica.project.v1",
		"project":{"description":null},
		"bootstrap":{"plan":{"goal":"partial","nodes":[]}},
		"tasks":[
			{"key":"TASK-1","status":"done"},
			{"key":"TASK-2","labels":[],"blocked_by_keys":[],"comments":[],"acceptance_criteria":[]}
		]
	}`))
	if err != nil {
		t.Fatalf("DecodeBundle sparse fields: %v", err)
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal sparse bundle: %v", err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("unmarshal sparse bundle: %v", err)
	}
	project := roundTrip["project"].(map[string]any)
	if value, exists := project["description"]; !exists || value != nil {
		t.Fatalf("explicit null project description = %#v, present=%t", value, exists)
	}
	if _, exists := project["title"]; exists {
		t.Fatal("omitted project title was serialized")
	}
	plan := roundTrip["bootstrap"].(map[string]any)["plan"].(map[string]any)
	if _, exists := plan["requirements"]; exists {
		t.Fatal("omitted plan requirements were serialized")
	}
	if nodes, exists := plan["nodes"]; !exists || len(nodes.([]any)) != 0 {
		t.Fatalf("explicit empty plan nodes = %#v, present=%t", nodes, exists)
	}
	tasks := roundTrip["tasks"].([]any)
	first := tasks[0].(map[string]any)
	if _, exists := first["labels"]; exists {
		t.Fatal("omitted task labels were serialized")
	}
	second := tasks[1].(map[string]any)
	for _, field := range []string{"labels", "blocked_by_keys", "comments", "acceptance_criteria"} {
		value, exists := second[field]
		if !exists || len(value.([]any)) != 0 {
			t.Fatalf("explicit empty %s = %#v, present=%t", field, value, exists)
		}
	}
}

func TestValidateBundleRejectsPlanCycles(t *testing.T) {
	bundle := Bundle{
		SchemaVersion: SchemaVersionV1,
		Project:       PortableProject{Title: "portable"},
		Tasks:         []PortableTask{},
		Bootstrap: &PortableBootstrap{Plan: &PortablePlan{
			Nodes: []PlanNode{{Key: "A", Title: "A"}, {Key: "B", Title: "B"}},
			Edges: []PlanEdge{{From: "A", To: "B"}, {From: "B", To: "A"}},
		}},
	}
	if err := ValidateBundle(bundle); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("ValidateBundle error = %v, want plan cycle error", err)
	}
}
