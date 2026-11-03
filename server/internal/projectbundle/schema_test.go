package projectbundle

import (
	"encoding/json"
	"reflect"
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

func TestBundleSchemaV2AndLegacyV1(t *testing.T) {
	const v2JSON = `{
		"schema_version":"multica.project.v2",
		"origin":{"workspace_id":"source-workspace","project_id":"source-project"},
		"project":{"title":"portable"},
		"approvals":[{
			"id":"approval-source-id",
			"idempotency_key":"approve-spec-2",
			"subject_type":"specification_revision",
			"subject_id":"spec-source-id",
			"subject_revision":2,
			"subject_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"decision":"approved",
			"actor_type":"human",
			"actor_id":"source-actor-id",
			"actor_display":"Ada Example",
			"rationale":"Reviewed source specification",
			"source_endpoint":"spec/approve",
			"origin":"local",
			"origin_ref":{"bundle":"source-bundle","event_id":"approval-source-id"},
			"acted_at":"2026-11-01T09:30:00Z"
		},{
			"id":"approval-imported-id",
			"idempotency_key":"import-source-approval",
			"subject_type":"plan",
			"subject_id":"plan-source-id",
			"subject_digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			"decision":"approved",
			"actor_type":"system",
			"actor_id":null,
			"actor_display":"Automated policy",
			"policy_ref":"low-risk-replan-v1",
			"rationale":"Source policy decision",
			"source_endpoint":"planning/replan",
			"origin":"imported",
			"origin_ref":{"original_origin":"local","source_event_id":"approval-imported-id"},
			"acted_at":"2026-11-01T09:35:00Z"
		}],
		"specification_revisions":[{
			"id":"spec-source-id",
			"revision":2,
			"source_kind":"planner",
			"source_ref":"proposal-2",
			"goal":"Track original specification provenance",
			"content":{"requirements":[{"id":"REQ-1","text":"Keep provenance"}],"legacy_field":{"kept":true}},
			"digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"created_at":"2026-11-01T09:00:00Z"
		}],
		"bootstrap":{"plan":{"nodes":[{"key":"implement","title":"Implement","requirement_ids":["REQ-1"]}]}},
		"tasks":[]
	}`

	bundle, err := DecodeBundle([]byte(v2JSON))
	if err != nil {
		t.Fatalf("DecodeBundle v2: %v", err)
	}
	if bundle.SchemaVersion != SchemaVersionV2 {
		t.Fatalf("schema version = %q, want %q", bundle.SchemaVersion, SchemaVersionV2)
	}
	if len(bundle.Approvals) != 2 {
		t.Fatalf("approval count = %d, want 2", len(bundle.Approvals))
	}
	approval := bundle.Approvals[0]
	if approval.ID != "approval-source-id" || approval.ActorID == nil || *approval.ActorID != "source-actor-id" || approval.Origin != "local" {
		t.Fatalf("approval provenance = %#v", approval)
	}
	importedApproval := bundle.Approvals[1]
	if importedApproval.ID != "approval-imported-id" || importedApproval.ActorID != nil || importedApproval.Origin != "imported" || importedApproval.PolicyRef == nil || *importedApproval.PolicyRef != "low-risk-replan-v1" {
		t.Fatalf("imported approval provenance = %#v", importedApproval)
	}
	if len(bundle.SpecificationRevisions) != 1 {
		t.Fatalf("specification revision count = %d, want 1", len(bundle.SpecificationRevisions))
	}
	revision := bundle.SpecificationRevisions[0]
	if revision.ID != "spec-source-id" || revision.Revision != 2 || revision.SourceKind != "planner" || revision.SourceRef == nil || *revision.SourceRef != "proposal-2" {
		t.Fatalf("specification revision provenance = %#v", revision)
	}
	var revisionContent map[string]any
	if err := json.Unmarshal(revision.Content, &revisionContent); err != nil {
		t.Fatalf("unmarshal specification revision content: %v", err)
	}
	legacyField, ok := revisionContent["legacy_field"].(map[string]any)
	if !ok || legacyField["kept"] != true {
		t.Fatalf("specification revision content lost unknown fields: %s", revision.Content)
	}
	if bundle.Bootstrap == nil || bundle.Bootstrap.Plan == nil || len(bundle.Bootstrap.Plan.Nodes) != 1 {
		t.Fatalf("plan nodes = %#v, want one node", bundle.Bootstrap)
	}
	node := bundle.Bootstrap.Plan.Nodes[0]
	if len(node.RequirementIDs) != 1 || node.RequirementIDs[0] != "REQ-1" {
		t.Fatalf("requirement_ids = %#v, want [REQ-1]", node.RequirementIDs)
	}

	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal v2 bundle: %v", err)
	}
	var portableJSON map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &portableJSON); err != nil {
		t.Fatalf("unmarshal marshaled v2 bundle: %v", err)
	}
	var portableApprovals []map[string]json.RawMessage
	if err := json.Unmarshal(portableJSON["approvals"], &portableApprovals); err != nil {
		t.Fatalf("unmarshal marshaled approval events: %v", err)
	}
	for _, field := range []string{"subject_revision", "actor_id", "policy_ref"} {
		if _, exists := portableApprovals[0][field]; !exists {
			t.Fatalf("approval JSON omitted nullable ledger field %q", field)
		}
	}
	if raw := portableApprovals[1]["actor_id"]; string(raw) != "null" {
		t.Fatalf("imported approval actor_id = %s, want null", raw)
	}
	roundTrip, err := DecodeBundle(encoded)
	if err != nil {
		t.Fatalf("DecodeBundle v2 round-trip: %v", err)
	}
	if !jsonValuesEqual(t, bundle.Approvals, roundTrip.Approvals) {
		t.Fatalf("approval fields changed during JSON round-trip: before=%#v after=%#v", bundle.Approvals, roundTrip.Approvals)
	}
	if !jsonValuesEqual(t, bundle.SpecificationRevisions, roundTrip.SpecificationRevisions) {
		t.Fatalf("specification revision fields changed during JSON round-trip: before=%#v after=%#v", bundle.SpecificationRevisions, roundTrip.SpecificationRevisions)
	}
	if roundTrip.Bootstrap == nil || roundTrip.Bootstrap.Plan == nil || len(roundTrip.Bootstrap.Plan.Nodes) != 1 {
		t.Fatalf("round-trip plan nodes = %#v, want one node", roundTrip.Bootstrap)
	}
	if !jsonValuesEqual(t, bundle.Bootstrap.Plan.Nodes[0].RequirementIDs, roundTrip.Bootstrap.Plan.Nodes[0].RequirementIDs) {
		t.Fatalf("requirement_ids changed during JSON round-trip: before=%#v after=%#v", bundle.Bootstrap.Plan.Nodes[0].RequirementIDs, roundTrip.Bootstrap.Plan.Nodes[0].RequirementIDs)
	}

	legacy, err := DecodeBundle([]byte(`{"schema_version":"multica.project.v1","project":{},"tasks":[{"key":"TASK-1","status":"done"}]}`))
	if err != nil {
		t.Fatalf("DecodeBundle legacy v1: %v", err)
	}
	if err := ValidateBundle(legacy); err != nil {
		t.Fatalf("ValidateBundle legacy v1: %v", err)
	}
	if legacy.SchemaVersion != SchemaVersionV1 || legacy.Approvals != nil || legacy.SpecificationRevisions != nil {
		t.Fatalf("legacy v1 bundle schema fields = %#v", legacy)
	}

	t.Log("PROJECT_BUNDLE_SCHEMA_V2_GATE_PASS")
}

func jsonValuesEqual(t *testing.T, before, after any) bool {
	t.Helper()
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		t.Fatalf("marshal before value: %v", err)
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		t.Fatalf("marshal after value: %v", err)
	}
	var beforeValue, afterValue any
	if err := json.Unmarshal(beforeJSON, &beforeValue); err != nil {
		t.Fatalf("decode before JSON: %v", err)
	}
	if err := json.Unmarshal(afterJSON, &afterValue); err != nil {
		t.Fatalf("decode after JSON: %v", err)
	}
	return reflect.DeepEqual(beforeValue, afterValue)
}
