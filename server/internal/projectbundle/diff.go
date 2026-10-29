package projectbundle

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

func BuildPreview(current, incoming Bundle, creatingProject bool) PortabilityPreview {
	preview := PortabilityPreview{Tasks: make([]TaskSyncDiff, 0, len(incoming.Tasks)), Warnings: []string{}}
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
	return preview
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
