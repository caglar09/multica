package projectbundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const SchemaVersionV1 = "multica.project.v1"

const maxPortableMetadataKeys = 50

var portableMetadataKeyPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]{0,63}$`)

type InvalidInputError struct{ Err error }

func (e *InvalidInputError) Error() string { return e.Err.Error() }
func (e *InvalidInputError) Unwrap() error { return e.Err }

func invalidInput(err error) error {
	if err == nil {
		return nil
	}
	var existing *InvalidInputError
	if errors.As(err, &existing) {
		return err
	}
	return &InvalidInputError{Err: err}
}

type Bundle struct {
	SchemaVersion string             `json:"schema_version"`
	ExportedAt    string             `json:"exported_at,omitempty"`
	Origin        PortableOrigin     `json:"origin,omitempty"`
	Project       PortableProject    `json:"project"`
	Resources     []PortableResource `json:"resources,omitempty"`
	Bootstrap     *PortableBootstrap `json:"bootstrap,omitempty"`
	Tasks         []PortableTask     `json:"tasks"`
}

type PortableOrigin struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
}

type PortableProject struct {
	Title       string            `json:"title"`
	Description *string           `json:"description,omitempty"`
	Icon        *string           `json:"icon,omitempty"`
	Status      *string           `json:"status,omitempty"`
	Priority    *string           `json:"priority,omitempty"`
	StartDate   *string           `json:"start_date,omitempty"`
	DueDate     *string           `json:"due_date,omitempty"`
	Lead        *PortableActorRef `json:"lead,omitempty"`
	present     map[string]bool
}

func (p *PortableProject) UnmarshalJSON(data []byte) error {
	type plain PortableProject
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	allowed := map[string]bool{
		"title": true, "description": true, "icon": true, "status": true,
		"priority": true, "start_date": true, "due_date": true, "lead": true,
	}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("unknown project field %q", key)
		}
	}
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*p = PortableProject(value)
	p.present = make(map[string]bool, len(fields))
	for key := range fields {
		p.present[key] = true
	}
	return nil
}

func (p PortableProject) MarshalJSON() ([]byte, error) {
	type plain PortableProject
	return marshalPresent(plain(p), map[string]any{
		"title": p.Title, "description": p.Description, "icon": p.Icon, "status": p.Status,
		"priority": p.Priority, "start_date": p.StartDate, "due_date": p.DueDate, "lead": p.Lead,
	}, p.present)
}

func (p PortableProject) Has(field string) bool { return p.present[field] }

type PortableResource struct {
	ResourceType string          `json:"resource_type"`
	Label        *string         `json:"label,omitempty"`
	ResourceRef  json.RawMessage `json:"resource_ref"`
	Position     *int32          `json:"position,omitempty"`
}

type PortableBootstrap struct {
	AutonomyMode  string              `json:"autonomy_mode,omitempty"`
	AutonomyLevel string              `json:"autonomy_level,omitempty"`
	Brief         string              `json:"brief,omitempty"`
	Knowledge     []PortableKnowledge `json:"knowledge,omitempty"`
	Policy        json.RawMessage     `json:"policy,omitempty"`
	Budget        json.RawMessage     `json:"budget,omitempty"`
	Plan          *PortablePlan       `json:"plan,omitempty"`
	present       map[string]bool
}

func (b *PortableBootstrap) UnmarshalJSON(data []byte) error {
	type plain PortableBootstrap
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	allowed := map[string]bool{
		"autonomy_mode": true, "autonomy_level": true, "brief": true,
		"knowledge": true, "policy": true, "budget": true, "plan": true,
	}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("unknown bootstrap field %q", key)
		}
	}
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*b = PortableBootstrap(value)
	b.present = make(map[string]bool, len(fields))
	for key := range fields {
		b.present[key] = true
	}
	return nil
}

func (b PortableBootstrap) MarshalJSON() ([]byte, error) {
	type plain PortableBootstrap
	return marshalPresent(plain(b), map[string]any{
		"autonomy_mode": b.AutonomyMode, "autonomy_level": b.AutonomyLevel, "brief": b.Brief,
		"knowledge": b.Knowledge, "policy": b.Policy, "budget": b.Budget, "plan": b.Plan,
	}, b.present)
}

func (b PortableBootstrap) Has(field string) bool { return b.present[field] }

type PortableKnowledge struct {
	Kind    string          `json:"kind"`
	Title   string          `json:"title"`
	Content json.RawMessage `json:"content"`
}

type PortablePlan struct {
	Goal          string                 `json:"goal,omitempty"`
	Requirements  []PlanRequirement      `json:"requirements,omitempty"`
	Specification *PortableSpecification `json:"specification,omitempty"`
	Nodes         []PlanNode             `json:"nodes,omitempty"`
	Edges         []PlanEdge             `json:"edges,omitempty"`
	present       map[string]bool
}

func (p *PortablePlan) UnmarshalJSON(data []byte) error {
	type plain PortablePlan
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	allowed := map[string]bool{"goal": true, "requirements": true, "specification": true, "nodes": true, "edges": true}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("unknown plan field %q", key)
		}
	}
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*p = PortablePlan(value)
	p.present = make(map[string]bool, len(fields))
	for key := range fields {
		p.present[key] = true
	}
	return nil
}

func (p PortablePlan) MarshalJSON() ([]byte, error) {
	type plain PortablePlan
	return marshalPresent(plain(p), map[string]any{
		"goal": p.Goal, "requirements": p.Requirements, "specification": p.Specification, "nodes": p.Nodes, "edges": p.Edges,
	}, p.present)
}

type PortableSpecificationRequirement struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Rationale string `json:"rationale"`
	Source    string `json:"source"`
}

type PortableSpecification struct {
	Goal                      string                             `json:"goal"`
	Summary                   string                             `json:"summary"`
	Requirements              []PortableSpecificationRequirement `json:"requirements"`
	NonFunctionalRequirements []string                           `json:"non_functional_requirements"`
	Constraints               []string                           `json:"constraints"`
	NonGoals                  []string                           `json:"non_goals"`
	DefinitionOfDone          []string                           `json:"definition_of_done"`
	AcceptanceExpectations    []string                           `json:"acceptance_expectations"`
}

type PlanRequirement struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Rationale string `json:"rationale,omitempty"`
}

type PlanNode struct {
	Key                string   `json:"key"`
	Title              string   `json:"title"`
	Description        string   `json:"description,omitempty"`
	Kind               string   `json:"kind,omitempty"`
	Status             string   `json:"status,omitempty"`
	Priority           int32    `json:"priority,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
}

type PlanEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type,omitempty"`
}

type PortableTask struct {
	Key                string             `json:"key"`
	OriginID           string             `json:"origin_id,omitempty"`
	Title              string             `json:"title"`
	Description        *string            `json:"description,omitempty"`
	Status             *string            `json:"status,omitempty"`
	StatusCategory     *string            `json:"status_category,omitempty"`
	Priority           *string            `json:"priority,omitempty"`
	ParentKey          *string            `json:"parent_key,omitempty"`
	Stage              *int32             `json:"stage,omitempty"`
	Position           *float64           `json:"position,omitempty"`
	BlockedByKeys      []string           `json:"blocked_by_keys,omitempty"`
	Labels             []PortableLabelRef `json:"labels,omitempty"`
	Assignee           *PortableActorRef  `json:"assignee,omitempty"`
	AcceptanceCriteria []string           `json:"acceptance_criteria,omitempty"`
	Metadata           map[string]any     `json:"metadata,omitempty"`
	Comments           []PortableComment  `json:"comments,omitempty"`
	present            map[string]bool
	markdownChecked    *bool
	attachmentCount    int
	identifier         string
	localIssueID       string
	portableKey        string
}

func (t *PortableTask) UnmarshalJSON(data []byte) error {
	type plain PortableTask
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	allowed := map[string]bool{
		"key": true, "origin_id": true, "title": true, "description": true, "status": true,
		"status_category": true, "priority": true, "parent_key": true, "stage": true,
		"position": true, "blocked_by_keys": true, "labels": true, "assignee": true,
		"acceptance_criteria": true, "metadata": true, "comments": true, "attachments": true,
	}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("unknown task field %q", key)
		}
	}
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var attachmentCount int
	if raw, ok := fields["attachments"]; ok && string(raw) != "null" {
		var attachments []json.RawMessage
		if err := json.Unmarshal(raw, &attachments); err != nil {
			return fmt.Errorf("attachments must be an array: %w", err)
		}
		attachmentCount = len(attachments)
	}
	*t = PortableTask(value)
	t.present = make(map[string]bool, len(fields))
	for key := range fields {
		t.present[key] = true
	}
	t.attachmentCount = attachmentCount
	return nil
}

func (t PortableTask) MarshalJSON() ([]byte, error) {
	type plain PortableTask
	fields := map[string]any{
		"key": t.Key, "origin_id": t.OriginID, "title": t.Title, "description": t.Description,
		"status": t.Status, "status_category": t.StatusCategory, "priority": t.Priority,
		"parent_key": t.ParentKey, "stage": t.Stage, "position": t.Position,
		"blocked_by_keys": t.BlockedByKeys, "labels": t.Labels, "assignee": t.Assignee,
		"acceptance_criteria": t.AcceptanceCriteria, "metadata": t.Metadata, "comments": t.Comments,
	}
	return marshalPresent(plain(t), fields, t.present)
}

func (t PortableTask) Has(field string) bool { return t.present[field] }

type PortableLabelRef struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type PortableActorRef struct {
	Type  string `json:"type,omitempty"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

type PortableComment struct {
	Key             string         `json:"key"`
	OriginID        string         `json:"origin_id,omitempty"`
	ParentKey       *string        `json:"parent_key,omitempty"`
	AuthorType      string         `json:"author_type,omitempty"`
	AuthorName      string         `json:"author_name,omitempty"`
	AuthorEmail     string         `json:"author_email,omitempty"`
	Type            string         `json:"type,omitempty"`
	Content         string         `json:"content"`
	CreatedAt       string         `json:"created_at,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	present         map[string]bool
	attachmentCount int
}

func (c *PortableComment) UnmarshalJSON(data []byte) error {
	type plain PortableComment
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	allowed := map[string]bool{
		"key": true, "origin_id": true, "parent_key": true, "author_type": true,
		"author_name": true, "author_email": true, "type": true, "content": true,
		"created_at": true, "metadata": true, "attachments": true,
	}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("unknown comment field %q", key)
		}
	}
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var attachmentCount int
	if raw, ok := fields["attachments"]; ok && string(raw) != "null" {
		var attachments []json.RawMessage
		if err := json.Unmarshal(raw, &attachments); err != nil {
			return fmt.Errorf("attachments must be an array: %w", err)
		}
		attachmentCount = len(attachments)
	}
	*c = PortableComment(value)
	c.present = make(map[string]bool, len(fields))
	for key := range fields {
		c.present[key] = true
	}
	c.attachmentCount = attachmentCount
	return nil
}

func (c PortableComment) MarshalJSON() ([]byte, error) {
	type plain PortableComment
	fields := map[string]any{
		"key": c.Key, "origin_id": c.OriginID, "parent_key": c.ParentKey,
		"author_type": c.AuthorType, "author_name": c.AuthorName, "author_email": c.AuthorEmail,
		"type": c.Type, "content": c.Content, "created_at": c.CreatedAt, "metadata": c.Metadata,
	}
	return marshalPresent(plain(c), fields, c.present)
}

func (b Bundle) HasUnsupportedAttachments() bool {
	for _, task := range b.Tasks {
		if task.attachmentCount > 0 {
			return true
		}
		for _, comment := range task.Comments {
			if comment.attachmentCount > 0 {
				return true
			}
		}
	}
	return false
}

func marshalPresent(value any, fields map[string]any, present map[string]bool) ([]byte, error) {
	if present == nil {
		return json.Marshal(value)
	}
	selected := make(map[string]any, len(present))
	for key := range present {
		if field, ok := fields[key]; ok {
			selected[key] = field
		}
	}
	return json.Marshal(selected)
}

type ImportRequest struct {
	Bundle              *Bundle                 `json:"bundle,omitempty"`
	Markdown            string                  `json:"markdown,omitempty"`
	ProjectTitleHint    string                  `json:"project_title_hint,omitempty"`
	Instruction         string                  `json:"instruction,omitempty"`
	ProjectContext      *ProjectContextPayload  `json:"project_context,omitempty"`
	DiscoveryReport     *ProjectDiscoveryReport `json:"discovery_report,omitempty"`
	DryRun              bool                    `json:"dry_run,omitempty"`
	CreateMissingLabels *bool                   `json:"create_missing_labels,omitempty"`
}

type SyncRequest struct {
	Bundle              *Bundle `json:"bundle,omitempty"`
	Markdown            string  `json:"markdown,omitempty"`
	DryRun              bool    `json:"dry_run,omitempty"`
	CreateMissingLabels *bool   `json:"create_missing_labels,omitempty"`
}

type PortabilityPreview struct {
	ProjectAction  string         `json:"project_action"`
	ProjectChanges []string       `json:"project_changes"`
	Tasks          []TaskSyncDiff `json:"tasks"`
	Warnings       []string       `json:"warnings"`
}

type TaskSyncDiff struct {
	Key     string   `json:"key"`
	Title   string   `json:"title"`
	Action  string   `json:"action"`
	Changes []string `json:"changes"`
}

type PortabilityResult struct {
	ProjectID       string                  `json:"project_id,omitempty"`
	CreatedCount    int                     `json:"created_count"`
	UpdatedCount    int                     `json:"updated_count"`
	UnchangedCount  int                     `json:"unchanged_count"`
	Warnings        []string                `json:"warnings"`
	Preview         *PortabilityPreview     `json:"preview,omitempty"`
	DiscoveryReport *ProjectDiscoveryReport `json:"discovery_report,omitempty"`
}

func DecodeBundle(raw []byte) (Bundle, error) {
	var bundle Bundle
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return Bundle{}, errors.New("bundle must contain one JSON value")
		}
		return Bundle{}, err
	}
	if err := ValidateBundle(bundle); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func ValidateBundle(bundle Bundle) error {
	if bundle.SchemaVersion != SchemaVersionV1 {
		return fmt.Errorf("schema_version must be %q", SchemaVersionV1)
	}
	for field, value := range map[string]*string{"start_date": bundle.Project.StartDate, "due_date": bundle.Project.DueDate} {
		if value != nil && strings.TrimSpace(*value) != "" {
			if _, err := time.Parse("2006-01-02", *value); err != nil {
				return fmt.Errorf("project.%s must use YYYY-MM-DD format", field)
			}
		}
	}
	tasks := make(map[string]PortableTask, len(bundle.Tasks))
	originIDs := make(map[string]string, len(bundle.Tasks))
	for i, task := range bundle.Tasks {
		if task.Key == "" || strings.TrimSpace(task.Key) != task.Key {
			return fmt.Errorf("tasks[%d].key is required and must not have surrounding whitespace", i)
		}
		if task.Has("title") && strings.TrimSpace(task.Title) == "" {
			return fmt.Errorf("tasks[%d].title cannot be empty when supplied", i)
		}
		if _, exists := tasks[task.Key]; exists {
			return fmt.Errorf("duplicate task key %q", task.Key)
		}
		if task.OriginID != "" {
			if prior, exists := originIDs[task.OriginID]; exists {
				return fmt.Errorf("tasks %q and %q share origin_id %q", prior, task.Key, task.OriginID)
			}
			originIDs[task.OriginID] = task.Key
		}
		if err := validatePortableMetadata(task.Metadata); err != nil {
			return fmt.Errorf("task %q metadata: %w", task.Key, err)
		}
		tasks[task.Key] = task
	}
	for _, task := range bundle.Tasks {
		if task.ParentKey != nil {
			if _, ok := tasks[*task.ParentKey]; !ok {
				return fmt.Errorf("task %q references unknown parent key %q", task.Key, *task.ParentKey)
			}
		}
		seen := make(map[string]bool, len(task.BlockedByKeys))
		for _, key := range task.BlockedByKeys {
			if _, ok := tasks[key]; !ok {
				return fmt.Errorf("task %q references unknown blocked_by key %q", task.Key, key)
			}
			if seen[key] {
				return fmt.Errorf("task %q has duplicate blocked_by key %q", task.Key, key)
			}
			if key == task.Key {
				return fmt.Errorf("task %q cannot block itself", task.Key)
			}
			seen[key] = true
		}
	}
	if err := validateTaskCycles(tasks); err != nil {
		return err
	}
	for _, task := range bundle.Tasks {
		if err := validateCommentGraph(task); err != nil {
			return err
		}
	}
	if bundle.Bootstrap != nil && bundle.Bootstrap.Plan != nil {
		if err := validatePlan(*bundle.Bootstrap.Plan); err != nil {
			return err
		}
		if bundle.Bootstrap.Plan.Specification != nil {
			if err := validatePortableSpecification(*bundle.Bootstrap.Plan.Specification); err != nil {
				return err
			}
		}
	}
	for i, resource := range bundle.Resources {
		var object map[string]json.RawMessage
		if len(resource.ResourceRef) == 0 || json.Unmarshal(resource.ResourceRef, &object) != nil || object == nil {
			return fmt.Errorf("resources[%d].resource_ref must be a JSON object", i)
		}
		if strings.TrimSpace(resource.ResourceType) == "" {
			return fmt.Errorf("resources[%d].resource_type is required", i)
		}
	}
	return nil
}

func validatePortableSpecification(spec PortableSpecification) error {
	if strings.TrimSpace(spec.Goal) == "" || strings.TrimSpace(spec.Summary) == "" || len(spec.Requirements) == 0 {
		return errors.New("project specification requires goal, summary, and at least one requirement")
	}
	seen := make(map[string]bool, len(spec.Requirements))
	for index, requirement := range spec.Requirements {
		if strings.TrimSpace(requirement.ID) == "" || strings.TrimSpace(requirement.Text) == "" || strings.TrimSpace(requirement.Rationale) == "" || strings.TrimSpace(requirement.Source) == "" {
			return fmt.Errorf("project specification requirements[%d] requires id, text, rationale, and source", index)
		}
		if seen[requirement.ID] {
			return fmt.Errorf("project specification contains duplicate requirement id %q", requirement.ID)
		}
		seen[requirement.ID] = true
	}
	for name, values := range map[string][]string{
		"non_functional_requirements": spec.NonFunctionalRequirements,
		"constraints":                 spec.Constraints,
		"non_goals":                   spec.NonGoals,
		"definition_of_done":          spec.DefinitionOfDone,
		"acceptance_expectations":     spec.AcceptanceExpectations,
	} {
		if err := validateDiscoveryList("project specification "+name, values, 100); err != nil {
			return err
		}
	}
	return nil
}

func validatePortableMetadata(metadata map[string]any) error {
	if err := validateMetadataKeys(metadata); err != nil {
		return err
	}
	for key, value := range metadata {
		switch value.(type) {
		case string, bool, float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		default:
			return fmt.Errorf("metadata value for %q must be a string, number, or bool", key)
		}
	}
	return nil
}

func validateMetadataKeys(metadata map[string]any) error {
	if len(metadata) > maxPortableMetadataKeys {
		return fmt.Errorf("at most %d metadata keys are allowed", maxPortableMetadataKeys)
	}
	for key := range metadata {
		if !portableMetadataKeyPattern.MatchString(key) {
			return fmt.Errorf("metadata key %q has an invalid format", key)
		}
	}
	return nil
}

// ValidateBundleForImport enforces the additional fields needed to create a
// project. Sync accepts sparse JSON patches, whose omitted titles are valid
// when the matching project or task already exists.
func ValidateBundleForImport(bundle Bundle) error {
	if err := ValidateBundle(bundle); err != nil {
		return err
	}
	if strings.TrimSpace(bundle.Project.Title) == "" {
		return errors.New("project.title is required")
	}
	for i, task := range bundle.Tasks {
		if strings.TrimSpace(task.Title) == "" {
			return fmt.Errorf("tasks[%d].title is required", i)
		}
	}
	return nil
}

func validateTaskCycles(tasks map[string]PortableTask) error {
	state := make(map[string]uint8, len(tasks))
	var visit func(string) error
	visit = func(key string) error {
		if state[key] == 1 {
			return fmt.Errorf("task dependency graph contains a cycle at %q", key)
		}
		if state[key] == 2 {
			return nil
		}
		state[key] = 1
		task := tasks[key]
		if task.ParentKey != nil {
			if err := visit(*task.ParentKey); err != nil {
				return err
			}
		}
		for _, blockedBy := range task.BlockedByKeys {
			if err := visit(blockedBy); err != nil {
				return err
			}
		}
		state[key] = 2
		return nil
	}
	for key := range tasks {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}

func validateCommentGraph(task PortableTask) error {
	comments := make(map[string]PortableComment, len(task.Comments))
	for _, comment := range task.Comments {
		if comment.Key == "" {
			return fmt.Errorf("task %q has a comment without a key", task.Key)
		}
		if strings.TrimSpace(comment.Content) == "" {
			return fmt.Errorf("task %q comment %q has empty content", task.Key, comment.Key)
		}
		if _, exists := comments[comment.Key]; exists {
			return fmt.Errorf("task %q has duplicate comment key %q", task.Key, comment.Key)
		}
		comments[comment.Key] = comment
	}
	state := make(map[string]uint8, len(comments))
	var visit func(string) error
	visit = func(key string) error {
		if state[key] == 1 {
			return fmt.Errorf("task %q comment graph contains a cycle at %q", task.Key, key)
		}
		if state[key] == 2 {
			return nil
		}
		state[key] = 1
		comment := comments[key]
		if comment.ParentKey != nil {
			if _, ok := comments[*comment.ParentKey]; !ok {
				return fmt.Errorf("task %q comment %q references unknown parent %q", task.Key, key, *comment.ParentKey)
			}
			if err := visit(*comment.ParentKey); err != nil {
				return err
			}
		}
		state[key] = 2
		return nil
	}
	for key := range comments {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}

func validatePlan(plan PortablePlan) error {
	nodes := make(map[string]bool, len(plan.Nodes))
	for _, node := range plan.Nodes {
		if strings.TrimSpace(node.Key) == "" || strings.TrimSpace(node.Title) == "" {
			return errors.New("plan nodes require key and title")
		}
		if nodes[node.Key] {
			return fmt.Errorf("duplicate plan node key %q", node.Key)
		}
		nodes[node.Key] = true
	}
	graph := make(map[string][]string, len(nodes))
	for _, edge := range plan.Edges {
		if edge.From == edge.To {
			return fmt.Errorf("plan node %q cannot depend on itself", edge.From)
		}
		if !nodes[edge.From] || !nodes[edge.To] {
			return fmt.Errorf("plan edge %q -> %q references an unknown node", edge.From, edge.To)
		}
		graph[edge.From] = append(graph[edge.From], edge.To)
	}
	state := make(map[string]uint8, len(nodes))
	var visit func(string) error
	visit = func(key string) error {
		if state[key] == 1 {
			return fmt.Errorf("plan dependency graph contains a cycle at %q", key)
		}
		if state[key] == 2 {
			return nil
		}
		state[key] = 1
		for _, next := range graph[key] {
			if err := visit(next); err != nil {
				return err
			}
		}
		state[key] = 2
		return nil
	}
	for key := range nodes {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}
