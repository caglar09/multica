package teamprovision

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
)

func TestSelectionJSONPreservesModelAndSkillMode(t *testing.T) {
	runtimeID, err := util.ParseUUID("11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	skillID, err := util.ParseUUID("22222222-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatal(err)
	}

	raw := selectionJSON([]RoleRuntimeSelection{
		{
			Role: "backend_engineer",
			RuntimeID: runtimeID,
			Model: "gpt-5.6-codex",
			SkillsSpecified: false,
		},
		{
			Role: "qa_engineer",
			RuntimeID: runtimeID,
			Model: "gpt-5.6-codex-mini",
			SkillIDs: []pgtype.UUID{skillID},
			SkillsSpecified: true,
		},
	})

	var got map[string]struct {
		RuntimeID string   `json:"runtime_id"`
		Model string        `json:"model"`
		SkillMode string    `json:"skill_mode"`
		SkillIDs []string   `json:"skill_ids"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["backend_engineer"].SkillMode != "inherit" {
		t.Fatalf("backend skill mode = %q, want inherit", got["backend_engineer"].SkillMode)
	}
	if len(got["backend_engineer"].SkillIDs) != 0 {
		t.Fatalf("inherit selection unexpectedly persisted skills: %v", got["backend_engineer"].SkillIDs)
	}
	if got["backend_engineer"].Model != "gpt-5.6-codex" {
		t.Fatalf("backend model = %q", got["backend_engineer"].Model)
	}
	if got["qa_engineer"].SkillMode != "custom" {
		t.Fatalf("qa skill mode = %q, want custom", got["qa_engineer"].SkillMode)
	}
	if len(got["qa_engineer"].SkillIDs) != 1 || got["qa_engineer"].SkillIDs[0] != util.UUIDToString(skillID) {
		t.Fatalf("qa skill ids = %v", got["qa_engineer"].SkillIDs)
	}
}
