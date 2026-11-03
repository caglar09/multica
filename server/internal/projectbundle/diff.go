package projectbundle

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

func BuildPreview(current, incoming Bundle, creatingProject bool) PortabilityPreview {
	preview := PortabilityPreview{
		Tasks: make([]TaskSyncDiff, 0, len(incoming.Tasks)), Warnings: []string{},
		ApprovalConflicts: []string{}, SpecificationChanges: []string{},
	}
	if creatingProject {
		preview.ProjectAction = "create"
		preview.ProjectChanges = projectFields(incoming.Project)
	} else {
		preview.ProjectChanges = changedProjectFields(current.Project, incoming.Project)
		if len(preview.ProjectChanges) == 0 {
			preview.ProjectAction = "unchanged"
		} else {
			preview.ProjectAction = "update"
		}
	}

	byPortable := make(map[string]PortableTask, len(current.Tasks))
	byIdentifier := make(map[string]PortableTask, len(current.Tasks))
	byOrigin := make(map[string]PortableTask, len(current.Tasks))
	for _, task := range current.Tasks {
		if task.Key != "" {
			byPortable[task.Key] = task
		}
		if task.OriginID != "" {
			byOrigin[task.OriginID] = task
		}
		if task.identifier != "" {
			byIdentifier[task.identifier] = task
		}
	}
	matched := make(map[string]bool, len(incoming.Tasks))
	for _, task := range incoming.Tasks {
		existing, ok := byPortable[task.Key]
		if !ok {
			existing, ok = byIdentifier[task.Key]
		}
		if !ok && task.OriginID != "" {
			existing, ok = byOrigin[task.OriginID]
		}
		if !ok {
			preview.Tasks = append(preview.Tasks, TaskSyncDiff{Key: task.Key, Title: task.Title, Action: "create", Changes: []string{"task"}})
			continue
		}
		if matched[existing.Key] {
			preview.Tasks = append(preview.Tasks, TaskSyncDiff{Key: task.Key, Title: task.Title, Action: "create", Changes: []string{"identity_conflict"}})
			continue
		}
		matched[existing.Key] = true
		changes := changedTaskFields(existing, task)
		action := "unchanged"
		if len(changes) > 0 {
			action = "update"
		}
		preview.Tasks = append(preview.Tasks, TaskSyncDiff{Key: task.Key, Title: task.Title, Action: action, Changes: changes})
	}
	preview.ApprovalConflicts, preview.approvalLedgerConflict, preview.approvalProvenanceConflict = approvalConflicts(current.Approvals, incoming.Approvals)
	var specificationProvenanceConflict bool
	preview.SpecificationChanges, preview.specificationHistoryConflict, specificationProvenanceConflict = specificationHistoryChanges(current, incoming)
	preview.approvalProvenanceConflict = preview.approvalProvenanceConflict || specificationProvenanceConflict
	return preview
}

func previewConflictError(preview PortabilityPreview) error {
	if len(preview.ApprovalConflicts) == 0 && !preview.specificationHistoryConflict {
		return nil
	}
	kinds := make([]error, 0, 3)
	if preview.approvalLedgerConflict {
		kinds = append(kinds, ErrApprovalLedgerConflict)
	}
	if preview.approvalProvenanceConflict {
		kinds = append(kinds, ErrApprovalProvenanceConflict)
	}
	if preview.specificationHistoryConflict {
		kinds = append(kinds, ErrSpecificationHistoryConflict)
	}
	return &PortabilityConflictError{
		ApprovalConflicts:    append([]string(nil), preview.ApprovalConflicts...),
		SpecificationChanges: append([]string(nil), preview.SpecificationChanges...),
		kinds:                kinds,
	}
}

func approvalConflicts(current, incoming []PortableApproval) ([]string, bool, bool) {
	conflicts := map[string]bool{}
	ledgerConflict := false
	provenanceConflict := false
	for _, approval := range incoming {
		matchedKey, payloadEqual := false, false
		for _, existing := range current {
			candidates := []PortableApproval{existing}
			if source, ok := importedSourceApproval(existing); ok {
				candidates = append(candidates, source)
			}
			for _, candidate := range candidates {
				matches := candidate.IdempotencyKey == approval.IdempotencyKey ||
					(existing.IdempotencyKey == "portable-import:"+approval.ID) ||
					(candidate.ID != "" && candidate.ID == approval.ID)
				if !matches {
					continue
				}
				matchedKey = true
				if sameApprovalIdempotencyPayload(candidate, approval) {
					payloadEqual = true
				}
			}
		}
		if matchedKey && !payloadEqual {
			ledgerConflict = true
			conflicts[fmt.Sprintf("idempotency key %q has a different approval payload", approval.IdempotencyKey)] = true
		}
	}
	all := make([]PortableApproval, 0, len(current)+len(incoming))
	all = append(all, current...)
	all = append(all, incoming...)
	type subjectRevision struct {
		subjectType string
		subjectID   string
		revision    int64
		revisionSet bool
	}
	digests := make(map[subjectRevision]string)
	for _, approval := range all {
		if approval.Decision != "approved" {
			continue
		}
		key := subjectRevision{subjectType: approval.SubjectType, subjectID: approval.SubjectID}
		if approval.SubjectRevision != nil {
			key.revision, key.revisionSet = *approval.SubjectRevision, true
		}
		if previous, ok := digests[key]; ok && previous != approval.SubjectDigest {
			provenanceConflict = true
			revision := "unspecified"
			if key.revisionSet {
				revision = fmt.Sprint(key.revision)
			}
			conflicts[fmt.Sprintf("approved %s %s revision %s has conflicting digests", key.subjectType, key.subjectID, revision)] = true
		} else {
			digests[key] = approval.SubjectDigest
		}
	}
	result := make([]string, 0, len(conflicts))
	for conflict := range conflicts {
		result = append(result, conflict)
	}
	sort.Strings(result)
	return result, ledgerConflict, provenanceConflict
}

func importedSourceApproval(approval PortableApproval) (PortableApproval, bool) {
	if approval.Origin != "imported" {
		return PortableApproval{}, false
	}
	var wrapper struct {
		SourceApproval *PortableApproval `json:"source_approval"`
	}
	if json.Unmarshal(approval.OriginRef, &wrapper) != nil || wrapper.SourceApproval == nil || wrapper.SourceApproval.IdempotencyKey == "" {
		return PortableApproval{}, false
	}
	return *wrapper.SourceApproval, true
}

func sameApprovalIdempotencyPayload(left, right PortableApproval) bool {
	return left.IdempotencyKey == right.IdempotencyKey &&
		left.SubjectType == right.SubjectType && left.SubjectID == right.SubjectID &&
		equalInt64Pointer(left.SubjectRevision, right.SubjectRevision) &&
		left.SubjectDigest == right.SubjectDigest && left.Decision == right.Decision &&
		left.ActorType == right.ActorType && equalStringPointer(left.ActorID, right.ActorID) &&
		left.ActorDisplay == right.ActorDisplay && equalStringPointer(left.PolicyRef, right.PolicyRef) &&
		left.Rationale == right.Rationale && left.SourceEndpoint == right.SourceEndpoint &&
		left.Origin == right.Origin && equalJSON(left.OriginRef, right.OriginRef)
}

func equalInt64Pointer(left, right *int64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func equalStringPointer(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func equalJSON(left, right json.RawMessage) bool {
	if len(left) == 0 || len(right) == 0 {
		return len(left) == len(right)
	}
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func specificationHistoryChanges(current, incoming Bundle) ([]string, bool, bool) {
	changes := map[string]bool{}
	historyConflict := false
	provenanceConflict := false
	currentBySourceID := make(map[string]PortableSpecificationRevision, len(current.SpecificationRevisions))
	for _, revision := range current.SpecificationRevisions {
		sourceID := revision.ID
		if revision.SourceRef != nil {
			if metadata, ok := decodePortableSpecificationImportRef(*revision.SourceRef); ok {
				sourceID = metadata.ID
			}
		}
		currentBySourceID[sourceID] = revision
	}
	for _, revision := range incoming.SpecificationRevisions {
		existing, ok := currentBySourceID[revision.ID]
		if !ok {
			changes[fmt.Sprintf("specification revision %d is new", revision.Revision)] = true
			continue
		}
		if existing.Digest != revision.Digest {
			changes[fmt.Sprintf("specification revision %d has a different digest", revision.Revision)] = true
			historyConflict = true
			provenanceConflict = true
		}
		if existing.SourceRef != nil {
			if metadata, ok := decodePortableSpecificationImportRef(*existing.SourceRef); ok && metadata.Revision != revision.Revision {
				changes[fmt.Sprintf("specification source %s changed its revision from %d to %d", revision.ID, metadata.Revision, revision.Revision)] = true
				historyConflict = true
			}
		}
	}
	head, hasHead := currentSpecificationHead(current)
	if hasHead && len(incoming.SpecificationRevisions) > 0 {
		importedHead, sourceRevision, imported := importedSpecificationHead(head)
		if imported {
			latestIncoming := incoming.SpecificationRevisions[0]
			for _, revision := range incoming.SpecificationRevisions[1:] {
				if revision.Revision > latestIncoming.Revision {
					latestIncoming = revision
				}
			}
			if latestIncoming.Revision < sourceRevision {
				changes[fmt.Sprintf("incoming specification history ends at revision %d, behind local imported head %d", latestIncoming.Revision, sourceRevision)] = true
				return sortedStringKeys(changes), true, provenanceConflict
			}
			if latestIncoming.Revision == sourceRevision && (latestIncoming.ID != importedHead.ID || latestIncoming.Digest != importedHead.Digest) {
				changes[fmt.Sprintf("incoming specification revision %d conflicts with the local imported head", sourceRevision)] = true
				historyConflict = true
			}
		}
	}
	if len(incoming.SpecificationRevisions) == 0 && incoming.Bootstrap != nil &&
		incoming.Bootstrap.Plan != nil && incoming.Bootstrap.Plan.Specification != nil {
		if head, hasHead := currentSpecificationHead(current); hasHead {
			if !sameLegacySpecification(head, *incoming.Bootstrap.Plan.Specification) {
				changes["legacy plan specification differs from the current specification head; include specification history to update it"] = true
				historyConflict = true
			}
		}
	}
	return sortedStringKeys(changes), historyConflict, provenanceConflict
}

func sameLegacySpecification(head PortableSpecificationRevision, incoming PortableSpecification) bool {
	if head.Goal != incoming.Goal {
		return false
	}
	var currentContent, incomingContent map[string]json.RawMessage
	if err := json.Unmarshal(head.Content, &currentContent); err != nil || currentContent == nil {
		return false
	}
	incomingRaw, err := json.Marshal(incoming)
	if err != nil || json.Unmarshal(incomingRaw, &incomingContent) != nil || incomingContent == nil {
		return false
	}
	// Goal is stored in its own database column; legacy rows may also carry a
	// duplicate JSON goal, so compare immutable specification content without it.
	delete(currentContent, "goal")
	delete(incomingContent, "goal")
	currentJSON, err := json.Marshal(currentContent)
	if err != nil {
		return false
	}
	incomingJSON, err := json.Marshal(incomingContent)
	return err == nil && jsonDocumentsEquivalent(currentJSON, incomingJSON)
}

func currentSpecificationHead(bundle Bundle) (PortableSpecificationRevision, bool) {
	if bundle.currentSpecificationRevisionID != "" {
		for _, revision := range bundle.SpecificationRevisions {
			if revision.ID == bundle.currentSpecificationRevisionID {
				return revision, true
			}
		}
	}
	if len(bundle.SpecificationRevisions) == 0 {
		return PortableSpecificationRevision{}, false
	}
	latest := bundle.SpecificationRevisions[0]
	for _, revision := range bundle.SpecificationRevisions[1:] {
		if revision.Revision > latest.Revision {
			latest = revision
		}
	}
	return latest, true
}

func importedSpecificationHead(head PortableSpecificationRevision) (PortableSpecificationRevision, int64, bool) {
	if head.SourceRef == nil {
		return PortableSpecificationRevision{}, 0, false
	}
	if metadata, ok := decodePortableSpecificationImportRef(*head.SourceRef); ok {
		return PortableSpecificationRevision{ID: metadata.ID, Revision: metadata.Revision, Digest: metadata.Digest}, metadata.Revision, true
	}
	if strings.HasPrefix(*head.SourceRef, "portable:") {
		return head, head.Revision, true
	}
	return PortableSpecificationRevision{}, 0, false
}

func sortedStringKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func projectFields(project PortableProject) []string {
	fields := []string{"title"}
	for _, field := range []string{"description", "icon", "status", "priority", "start_date", "due_date", "lead"} {
		if project.Has(field) || project.present == nil {
			fields = append(fields, field)
		}
	}
	return fields
}

func changedProjectFields(current, incoming PortableProject) []string {
	fields := make([]string, 0, 8)
	check := func(field string, a, b any) {
		if incoming.Has(field) || incoming.present == nil {
			if !reflect.DeepEqual(a, b) {
				fields = append(fields, field)
			}
		}
	}
	check("title", current.Title, incoming.Title)
	check("description", current.Description, incoming.Description)
	check("icon", current.Icon, incoming.Icon)
	check("status", current.Status, incoming.Status)
	check("priority", current.Priority, incoming.Priority)
	check("start_date", current.StartDate, incoming.StartDate)
	check("due_date", current.DueDate, incoming.DueDate)
	check("lead", current.Lead, incoming.Lead)
	sort.Strings(fields)
	return fields
}

func changedTaskFields(current, incoming PortableTask) []string {
	fields := make([]string, 0, 12)
	check := func(field string, a, b any) {
		if incoming.Has(field) || incoming.present == nil {
			if !reflect.DeepEqual(a, b) {
				fields = append(fields, field)
			}
		}
	}
	check("title", current.Title, incoming.Title)
	check("description", current.Description, incoming.Description)
	check("status", current.Status, incoming.Status)
	check("status_category", current.StatusCategory, incoming.StatusCategory)
	check("priority", current.Priority, incoming.Priority)
	check("parent_key", current.ParentKey, incoming.ParentKey)
	check("stage", current.Stage, incoming.Stage)
	check("position", current.Position, incoming.Position)
	check("blocked_by_keys", current.BlockedByKeys, incoming.BlockedByKeys)
	check("labels", current.Labels, incoming.Labels)
	check("assignee", current.Assignee, incoming.Assignee)
	check("acceptance_criteria", current.AcceptanceCriteria, incoming.AcceptanceCriteria)
	if incoming.Has("metadata") || incoming.present == nil {
		for key, value := range incoming.Metadata {
			if current.Metadata == nil || !reflect.DeepEqual(current.Metadata[key], value) {
				fields = append(fields, "metadata."+key)
			}
		}
	}
	if incoming.Has("comments") || incoming.present == nil {
		existingComments := make(map[string]bool, len(current.Comments))
		for _, comment := range current.Comments {
			existingComments[comment.Key] = true
		}
		for _, comment := range incoming.Comments {
			if !existingComments[comment.Key] {
				fields = append(fields, "comments")
				break
			}
		}
	}
	currentPortableKey := current.portableKey
	if currentPortableKey == "" && current.Metadata != nil {
		currentPortableKey, _ = current.Metadata["multica_portable_key"].(string)
	}
	if incoming.Key != "" && currentPortableKey != incoming.Key {
		fields = append(fields, "metadata.multica_portable_key")
	}
	sort.Strings(fields)
	return fields
}

func addWarning(preview *PortabilityPreview, warning string) {
	if strings.TrimSpace(warning) == "" {
		return
	}
	for _, current := range preview.Warnings {
		if current == warning {
			return
		}
	}
	preview.Warnings = append(preview.Warnings, warning)
}

func taskChangeError(key, field string, err error) error {
	return fmt.Errorf("task %q field %s: %w", key, field, err)
}
