package projectorchestration

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func traceabilitySpecification() ProjectSpecification {
	return ProjectSpecification{
		Goal:    "Deliver the requested project",
		Summary: "A project with stable requirements",
		Requirements: []SpecificationRequirement{
			{ID: "REQ-1", Text: "Preserve requirement identity", Rationale: "Traceability needs stable IDs", Source: "brief"},
			{ID: "REQ-2", Text: "Keep the full specification", Rationale: "The planner must not discard context", Source: "brief"},
		},
		NonFunctionalRequirements: []string{"Keep the planner deterministic"},
		Constraints:               []string{"Keep the existing requirements string array"},
		NonGoals:                  []string{"Do not redesign the project UI"},
		DefinitionOfDone:          []string{"Every requirement has implementation and review coverage"},
		AcceptanceExpectations:    []string{"The complete specification survives persistence"},
	}
}

func traceabilityPlan() Plan {
	spec := traceabilitySpecification()
	implementationIDs := []string{"REQ-1", "REQ-2"}
	verificationIDs := []string{"REQ-1", "REQ-2"}
	return Plan{
		Version:       CurrentPlanVersion,
		Goal:          spec.Goal,
		Specification: spec.LegacySpecification(),
		Policy:        DefaultPolicy(),
		Nodes: []NodeSpec{
			{Key: "implement", Kind: NodeImplementation, Title: "Implement requirements", RequirementIDs: implementationIDs, AcceptanceCriteria: []string{"Both requirements are implemented"}, Risk: RiskMedium, MaxAttempts: 3},
			{Key: "review", Kind: NodeReview, Title: "Review the implementation", RequirementIDs: verificationIDs, AcceptanceCriteria: []string{"Both requirements pass independent review"}, Risk: RiskLow, MaxAttempts: 3},
		},
		Edges: []EdgeSpec{{From: "implement", To: "review", Type: DependencyHard}},
	}
}

func TestProjectSpecificationLegacyProjectionPreservesRichFields(t *testing.T) {
	spec := traceabilitySpecification()
	legacy := spec.LegacySpecification()

	wantTexts := []string{"Preserve requirement identity", "Keep the full specification"}
	if !reflect.DeepEqual(legacy.Requirements, wantTexts) {
		t.Fatalf("legacy requirements = %#v, want %#v", legacy.Requirements, wantTexts)
	}
	if !reflect.DeepEqual(legacy.RequirementDetails, spec.Requirements) {
		t.Fatalf("requirement details = %#v, want %#v", legacy.RequirementDetails, spec.Requirements)
	}
	if !reflect.DeepEqual(legacy.NonGoals, spec.NonGoals) {
		t.Fatalf("non-goals = %#v, want %#v", legacy.NonGoals, spec.NonGoals)
	}
	if !reflect.DeepEqual(legacy.AcceptanceExpectations, spec.AcceptanceExpectations) {
		t.Fatalf("acceptance expectations = %#v, want %#v", legacy.AcceptanceExpectations, spec.AcceptanceExpectations)
	}
	if !reflect.DeepEqual(legacy.NonFunctionalRequirements, spec.NonFunctionalRequirements) ||
		!reflect.DeepEqual(legacy.Constraints, spec.Constraints) ||
		!reflect.DeepEqual(legacy.DefinitionOfDone, spec.DefinitionOfDone) {
		t.Fatal("legacy projection lost non-functional requirements, constraints, or definition of done")
	}

	plan := applyAuthoritativeSpecification(Plan{Goal: "planner-generated goal"}, &spec, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if plan.Goal != spec.Goal {
		t.Fatalf("authoritative goal = %q, want %q", plan.Goal, spec.Goal)
	}
	if plan.SpecificationRevisionID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("authoritative specification revision = %q", plan.SpecificationRevisionID)
	}

	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy specification: %v", err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("decode legacy specification wire shape: %v", err)
	}
	var requirements []string
	if err := json.Unmarshal(wire["requirements"], &requirements); err != nil {
		t.Fatalf("requirements must remain a string array: %v", err)
	}
	var restored Specification
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal rich specification: %v", err)
	}
	if !reflect.DeepEqual(restored, legacy) {
		t.Fatalf("rich specification round-trip mismatch:\n got %#v\nwant %#v", restored, legacy)
	}

	tracedPlan := traceabilityPlan()
	planJSON, err := json.Marshal(tracedPlan)
	if err != nil {
		t.Fatalf("marshal traced plan: %v", err)
	}
	var restoredPlan Plan
	if err := json.Unmarshal(planJSON, &restoredPlan); err != nil {
		t.Fatalf("unmarshal traced plan: %v", err)
	}
	if !reflect.DeepEqual(restoredPlan, tracedPlan) {
		t.Fatalf("plan specification/node traceability round-trip mismatch:\n got %#v\nwant %#v", restoredPlan, tracedPlan)
	}

	legacyWire := []byte(`{"version":1,"goal":"Legacy plan","specification":{"summary":"Legacy summary","requirements":["Legacy requirement"],"non_functional_requirements":[],"constraints":[],"definition_of_done":["Done"]},"policy":{},"nodes":[],"edges":[]}`)
	var legacyPlan Plan
	if err := json.Unmarshal(legacyWire, &legacyPlan); err != nil {
		t.Fatalf("existing plan wire shape is no longer readable: %v", err)
	}
	if len(legacyPlan.Specification.Requirements) != 1 || legacyPlan.Specification.Requirements[0] != "Legacy requirement" {
		t.Fatalf("legacy requirements = %#v", legacyPlan.Specification.Requirements)
	}
}

func TestRequirementCoverageRequiresImplementationAndIndependentVerification(t *testing.T) {
	plan := traceabilityPlan()
	if err := ValidateRequirementCoverage(plan); err != nil {
		t.Fatalf("complete requirement coverage rejected: %v", err)
	}

	plan.Nodes = plan.Nodes[:1]
	err := ValidateRequirementCoverage(plan)
	if !errors.Is(err, ErrUncoveredRequirement) || !strings.Contains(err.Error(), "independent review or QA") {
		t.Fatalf("missing verification error = %v, want independent verification failure", err)
	}

	plan = traceabilityPlan()
	plan.Nodes[0].Kind = NodeArchitecture
	err = ValidateRequirementCoverage(plan)
	if !errors.Is(err, ErrUncoveredRequirement) || !strings.Contains(err.Error(), "implementation node") {
		t.Fatalf("missing implementation error = %v, want implementation failure", err)
	}
}

func TestProjectSpecificationFromPlanPreservesRichFieldsWithoutInventingIDs(t *testing.T) {
	plan := traceabilityPlan()
	converted, err := ProjectSpecificationFromPlan(plan)
	if err != nil {
		t.Fatalf("ProjectSpecificationFromPlan() error = %v", err)
	}
	if !reflect.DeepEqual(converted, traceabilitySpecification()) {
		t.Fatalf("converted specification = %#v", converted)
	}

	plan.Specification.RequirementDetails = nil
	_, err = ProjectSpecificationFromPlan(plan)
	if err == nil || !strings.Contains(err.Error(), "at least one requirement") {
		t.Fatalf("legacy plan without rich IDs error = %v, want rejected bootstrap", err)
	}
}

func TestRequirementCoverageRejectsUnknownAndMismatchedRequirementIDs(t *testing.T) {
	legacyWithMappings := traceabilityPlan()
	legacyWithMappings.Specification.RequirementDetails = nil
	if err := ValidateRequirementCoverage(legacyWithMappings); !errors.Is(err, ErrInvalidRequirementCoverage) || !strings.Contains(err.Error(), "without a rich specification") {
		t.Fatalf("requirement mappings without rich specification error = %v", err)
	}

	plan := traceabilityPlan()
	plan.Nodes[0].RequirementIDs = append(plan.Nodes[0].RequirementIDs, "REQ-UNKNOWN")
	if err := ValidateRequirementCoverage(plan); !errors.Is(err, ErrInvalidRequirementCoverage) || !strings.Contains(err.Error(), "unknown requirement") {
		t.Fatalf("unknown requirement error = %v", err)
	}

	plan = traceabilityPlan()
	plan.Specification.Requirements[0] = "different text"
	if err := ValidateRequirementCoverage(plan); !errors.Is(err, ErrInvalidRequirementCoverage) || !strings.Contains(err.Error(), "text mismatch") {
		t.Fatalf("mismatched rich/legacy text error = %v", err)
	}
}

func TestPlanMutationTraceabilityPreservesIDsAndRejectsCoverageRegression(t *testing.T) {
	plan := traceabilityPlan()
	implementation, review := plan.Nodes[0], plan.Nodes[1]
	logicalNodes := []LogicalPlanNode{
		{LogicalNodeID: "11111111-1111-1111-1111-111111111111", Spec: implementation, Status: "pending"},
		{LogicalNodeID: "22222222-2222-2222-2222-222222222222", Spec: review, Status: "pending"},
	}
	updated := implementation
	updated.Title = "Implement both requirements"
	result, err := ApplyPlanMutation(plan, logicalNodes, []PlanMutationOperation{{
		Operation:           MutationUpdateNode,
		TargetLogicalNodeID: logicalNodes[0].LogicalNodeID,
		Node:                &updated,
	}})
	if err != nil {
		t.Fatalf("ApplyPlanMutation() error = %v", err)
	}
	if !reflect.DeepEqual(result.Plan.Nodes[0].RequirementIDs, implementation.RequirementIDs) {
		t.Fatalf("mutation lost implementation requirement IDs: got %#v want %#v", result.Plan.Nodes[0].RequirementIDs, implementation.RequirementIDs)
	}
	if !reflect.DeepEqual(result.Plan.Specification.RequirementDetails, plan.Specification.RequirementDetails) ||
		!reflect.DeepEqual(result.Plan.Specification.NonGoals, plan.Specification.NonGoals) ||
		!reflect.DeepEqual(result.Plan.Specification.AcceptanceExpectations, plan.Specification.AcceptanceExpectations) {
		t.Fatal("plan mutation lost rich specification fields")
	}

	updated.RequirementIDs = nil
	_, err = ApplyPlanMutation(plan, logicalNodes, []PlanMutationOperation{{
		Operation:           MutationUpdateNode,
		TargetLogicalNodeID: logicalNodes[0].LogicalNodeID,
		Node:                &updated,
	}})
	if !errors.Is(err, ErrUncoveredRequirement) {
		t.Fatalf("mutation that removes implementation coverage error = %v, want traceability failure", err)
	}
}

func TestPlannerPromptTraceabilityUsesStableRequirementIDs(t *testing.T) {
	specification := traceabilitySpecification()
	input := PlanningInput{ProjectSpecification: &specification, SpecificationRevisionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}
	prompt := projectPlannerUserPrompt(input)
	for _, value := range []string{"requirement_details", "requirement_ids", "REQ-1", "REQ-2", "independent review or QA"} {
		if !strings.Contains(prompt, value) {
			t.Errorf("planner prompt is missing %q", value)
		}
	}
}

type traceabilityPlanExecutor struct {
	output  string
	calls   int
	prompts []string
}

func (e *traceabilityPlanExecutor) ExecuteProjectPlan(_ context.Context, _ PlanningInput, _, userPrompt string) (RuntimeExecution, error) {
	e.calls++
	e.prompts = append(e.prompts, userPrompt)
	return RuntimeExecution{Output: e.output, Provider: "test", Model: "test"}, nil
}

func TestPlannerOverlaysAuthoritativeSpecificationAndAcceptsCompleteCoverage(t *testing.T) {
	specification := traceabilitySpecification()
	plan := traceabilityPlan()
	plan.Goal = "Planner generated a different goal"
	plan.Specification = Specification{Summary: "Planner generated a different specification", Requirements: []string{"model requirement"}, DefinitionOfDone: []string{"model definition"}}
	output, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal planner output: %v", err)
	}
	executor := &traceabilityPlanExecutor{output: string(output)}
	planner := NewPlanner(executor, 10, DefaultPolicy())
	result, _, err := planner.Plan(context.Background(), PlanningInput{ProjectSpecification: &specification, SpecificationRevisionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"})
	if err != nil {
		t.Fatalf("Planner.Plan() error = %v", err)
	}
	if result.Goal != specification.Goal || !reflect.DeepEqual(result.Specification.RequirementDetails, specification.Requirements) {
		t.Fatalf("planner did not preserve authoritative specification: %#v", result)
	}
	if executor.calls != 1 {
		t.Fatalf("planner made %d calls, want 1", executor.calls)
	}
}

func TestPlannerRejectsRequirementWithoutIndependentVerificationAfterRepair(t *testing.T) {
	specification := traceabilitySpecification()
	plan := traceabilityPlan()
	plan.Nodes[1].RequirementIDs = nil
	output, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal planner output: %v", err)
	}
	executor := &traceabilityPlanExecutor{output: string(output)}
	planner := NewPlanner(executor, 10, DefaultPolicy())
	_, _, err = planner.Plan(context.Background(), PlanningInput{ProjectSpecification: &specification, SpecificationRevisionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"})
	if !errors.Is(err, ErrUncoveredRequirement) {
		t.Fatalf("Planner.Plan() error = %v, want uncovered requirement", err)
	}
	if executor.calls != 2 {
		t.Fatalf("planner made %d calls after repair, want 2", executor.calls)
	}
	if !strings.Contains(executor.prompts[1], "REQ-1") || !strings.Contains(executor.prompts[1], "requirement_ids") {
		t.Fatal("repair prompt omitted authoritative IDs or requirement mapping instructions")
	}
}
