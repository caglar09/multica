package projectorchestration

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCompileProjectLeaderProposalRejectsMaterializedNodeMutation(t *testing.T) {
	id := uuid.New()
	proposal := ProjectChangeProposal{
		ProposalID: "proposal-1", Summary: "Change scope", Rationale: "new requirement", BasePlanRevision: 3,
		Operations: []ProjectChangeOperation{{
			Type: ProjectLeaderOpUpdateNotStartedNode, NodeID: &id, Reason: "revise implementation",
			Patch: map[string]any{"title": "Changed title"},
		}},
	}
	_, err := CompileProjectLeaderProposal(proposal, []LogicalPlanNode{{
		LogicalNodeID: id.String(), MaterializedIssueID: uuid.NewString(), Status: "ready",
		Spec: NodeSpec{Key: "implementation", Kind: NodeImplementation, Title: "Original"},
	}})
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("expected materialized node to be immutable, got %v", err)
	}
}

func TestCompileProjectLeaderProposalRejectsCompletedDependencyRewrite(t *testing.T) {
	completedID := uuid.New()
	pendingID := uuid.New()
	proposal := ProjectChangeProposal{
		ProposalID: "proposal-2", Summary: "Rewrite dependency", Rationale: "new ordering", BasePlanRevision: 4,
		Operations: []ProjectChangeOperation{{
			Type: ProjectLeaderOpRemoveDependency, Reason: "remove old edge",
			Dependency: &EdgeSpec{From: "architecture", To: "implementation", Type: DependencyHard},
		}},
	}
	_, err := CompileProjectLeaderProposal(proposal, []LogicalPlanNode{
		{LogicalNodeID: completedID.String(), Status: "completed", Spec: NodeSpec{Key: "architecture", Kind: NodeArchitecture, Title: "Architecture"}},
		{LogicalNodeID: pendingID.String(), Status: "pending", Spec: NodeSpec{Key: "implementation", Kind: NodeImplementation, Title: "Implementation"}},
	})
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("expected completed dependency endpoint to be immutable, got %v", err)
	}
}

func TestCompileProjectLeaderProposalAllowsPendingPatch(t *testing.T) {
	id := uuid.New()
	proposal := ProjectChangeProposal{
		ProposalID: "proposal-3", Summary: "Tune task", Rationale: "clarify acceptance", BasePlanRevision: 5,
		Operations: []ProjectChangeOperation{{
			Type: ProjectLeaderOpUpdateNotStartedNode, NodeID: &id, Reason: "clarify",
			Patch: map[string]any{"title": "Updated"},
		}},
	}
	ops, err := CompileProjectLeaderProposal(proposal, []LogicalPlanNode{{
		LogicalNodeID: id.String(), Status: "pending",
		Spec: NodeSpec{Key: "implementation", Kind: NodeImplementation, Title: "Original", Description: "Do work", Risk: RiskLow, MaxAttempts: 3},
	}})
	if err != nil { t.Fatalf("compile proposal: %v", err) }
	if len(ops) != 1 || ops[0].Operation != MutationUpdateNode || ops[0].Node == nil || ops[0].Node.Title != "Updated" {
		t.Fatalf("unexpected compiled operations: %#v", ops)
	}
}

func TestValidateProjectLeaderSpecificationProposal(t *testing.T) {
	proposal := ProjectLeaderSpecificationProposal{
		ProposalID: "initial-spec-1",
		Rationale:  "The user's brief needs explicit, testable requirements before planning.",
		Specification: ProjectSpecification{
			Goal:    "Create a reviewable project plan",
			Summary: "Capture scope before generating executable work.",
			Requirements: []SpecificationRequirement{{
				ID: "REQ-001", Text: "The PM records each requirement with its source.",
				Rationale: "Reviewers need to trace requirements to user intent.", Source: "project brief",
			}},
			DefinitionOfDone: []string{"An owner can review the specification before planning."},
		},
	}
	if err := ValidateProjectLeaderResponse(ProjectLeaderResponse{
		Message:               "Here is the initial specification draft.",
		SpecificationProposal: &proposal,
	}); err != nil {
		t.Fatalf("validate initial specification proposal: %v", err)
	}

	invalid := proposal
	invalid.Specification.Requirements = nil
	if err := ValidateProjectLeaderSpecificationProposal(invalid); err == nil {
		t.Fatal("validate initial specification without requirements, want error")
	}

	if err := ValidateProjectLeaderResponse(ProjectLeaderResponse{
		Message:               "Ambiguous response.",
		Proposal:              &ProjectChangeProposal{ProposalID: "change-1"},
		SpecificationProposal: &proposal,
	}); err == nil {
		t.Fatal("validate response with two proposal types, want error")
	}
}

func TestValidateProjectChangeProposalRejectsUnknownPatchField(t *testing.T) {
	id := uuid.New()
	err := ValidateProjectChangeProposal(ProjectChangeProposal{
		ProposalID: "proposal-4", Summary: "Unsafe patch", Rationale: "test", BasePlanRevision: 1,
		Operations: []ProjectChangeOperation{{Type: ProjectLeaderOpUpdateNotStartedNode, NodeID: &id, Reason: "test", Patch: map[string]any{"status": "completed"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported node patch field") {
		t.Fatalf("expected strict patch allowlist error, got %v", err)
	}
}
