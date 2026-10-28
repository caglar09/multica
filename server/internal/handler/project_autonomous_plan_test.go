package handler

import (
	"encoding/json"
	"testing"
)

func TestAutonomousProjectPlanNodeHierarchyDefaults(t *testing.T) {
	raw, err := json.Marshal(AutonomousProjectPlanNodeResponse{})
	if err != nil {
		t.Fatalf("marshal default plan node response: %v", err)
	}
	var node map[string]any
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatalf("decode default plan node response: %v", err)
	}
	if value, ok := node["is_epic"].(bool); !ok || value {
		t.Fatalf("is_epic default = %#v, want false", node["is_epic"])
	}
	if value, ok := node["parent_epic_key"]; !ok || value != nil {
		t.Fatalf("parent_epic_key default = %#v, want null", value)
	}
}

func TestMarkAutonomousSnapshotPartialDeduplicatesMissingSections(t *testing.T) {
	response := AutonomousProjectResponse{}
	markAutonomousSnapshotPartial(&response, "activity.tasks")
	markAutonomousSnapshotPartial(&response, "activity.tasks")
	markAutonomousSnapshotPartial(&response, "deployments")

	if !response.Partial {
		t.Fatal("snapshot should be marked partial")
	}
	if len(response.MissingSections) != 2 || response.MissingSections[0] != "activity.tasks" || response.MissingSections[1] != "deployments" {
		t.Fatalf("missing sections = %#v", response.MissingSections)
	}
}
