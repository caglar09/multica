package projectbundle

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxDiscoveryFiles       = 256
	maxDiscoveryFileBytes   = 256 << 10
	maxDiscoveryTotalBytes  = 4 << 20
	maxDiscoveryReportItems = 100
)

type ProjectContextPayload struct {
	RootName  string               `json:"root_name"`
	Files     []ProjectContextFile `json:"files"`
	Warnings  []string             `json:"warnings,omitempty"`
	LocalPath string               `json:"local_path,omitempty"`
	DaemonID  string               `json:"daemon_id,omitempty"`
}

type ProjectContextFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type CollectOptions struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

type ProjectDiscoveryReport struct {
	Project             DiscoveredProject    `json:"project"`
	Boundaries          DiscoveredBoundaries `json:"boundaries"`
	ImplementedFeatures []DiscoveredFeature  `json:"implemented_features"`
	NextTasks           []DiscoveredTask     `json:"next_tasks"`
}

type DiscoveredProject struct {
	Title               string `json:"title"`
	Purpose             string `json:"purpose"`
	Goal                string `json:"goal"`
	ArchitectureSummary string `json:"architecture_summary"`
}

type DiscoveredBoundaries struct {
	InScope          []string `json:"in_scope"`
	OutOfScope       []string `json:"out_of_scope"`
	Constraints      []string `json:"constraints"`
	DefinitionOfDone []string `json:"definition_of_done"`
}

type DiscoveredFeature struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Evidence    []string `json:"evidence"`
}

type DiscoveredTask struct {
	Key                string   `json:"key"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Priority           string   `json:"priority"`
	Stage              int32    `json:"stage"`
	ParentKey          string   `json:"parent_key"`
	BlockedByKeys      []string `json:"blocked_by_keys"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
}

var sensitiveProjectFile = regexp.MustCompile(`(?i)((^|/)\.env($|\.)|(^|/)(credentials?|secrets?)(\.|$)|(^|/)(id_rsa|id_ed25519|\.netrc|\.npmrc|\.pypirc|\.dockerconfigjson|kubeconfig)($|\.)|\.(pem|key|p12|pfx|tfstate)$)`)

var excludedProjectDirectories = map[string]bool{
	".git": true, ".multica": true, ".svn": true, ".hg": true,
	"node_modules": true, "vendor": true, "dist": true, "build": true,
	"target": true, "out": true, ".next": true, ".nuxt": true,
	"coverage": true, ".cache": true, ".turbo": true, ".venv": true,
	"venv": true, "__pycache__": true, ".ssh": true, ".aws": true, ".kube": true,
	"credentials": true, "secrets": true,
}

var windowsDrivePath = regexp.MustCompile(`^[A-Za-z]:`)

func CollectProjectContext(rootDir string, opts CollectOptions) (ProjectContextPayload, error) {
	root, err := filepath.Abs(rootDir)
	if err != nil {
		return ProjectContextPayload{}, fmt.Errorf("resolve project directory: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return ProjectContextPayload{}, fmt.Errorf("stat project directory: %w", err)
	}
	if !info.IsDir() {
		return ProjectContextPayload{}, errors.New("project input must be a directory")
	}
	maxFiles := opts.MaxFiles
	if maxFiles <= 0 {
		maxFiles = maxDiscoveryFiles
	}
	maxFileBytes := opts.MaxFileBytes
	if maxFileBytes <= 0 {
		maxFileBytes = maxDiscoveryFileBytes
	}
	maxTotalBytes := opts.MaxTotalBytes
	if maxTotalBytes <= 0 {
		maxTotalBytes = maxDiscoveryTotalBytes
	}
	payload := ProjectContextPayload{RootName: filepath.Base(root), Files: []ProjectContextFile{}, LocalPath: root}
	var totalBytes int64
	warnings := map[string]bool{}
	addWarning := func(message string) {
		warnings[message] = true
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			addWarning("Some project paths could not be read and were skipped.")
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if excludedProjectDirectories[strings.ToLower(entry.Name())] {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		if sensitiveProjectFile.MatchString(rel) {
			addWarning("Sensitive files were excluded from project analysis.")
			return nil
		}
		fileInfo, err := entry.Info()
		if err != nil {
			addWarning("Some project files could not be read and were skipped.")
			return nil
		}
		if fileInfo.Size() > maxFileBytes {
			addWarning("Large files were excluded from project analysis.")
			return nil
		}
		if len(payload.Files) >= maxFiles || totalBytes+fileInfo.Size() > maxTotalBytes {
			addWarning("Project analysis input was truncated to stay within safe size limits.")
			return fs.SkipAll
		}
		file, err := os.Open(path)
		if err != nil {
			addWarning("Some project files could not be read and were skipped.")
			return nil
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			addWarning("Some project files could not be read and were skipped.")
			return nil
		}
		if int64(len(data)) > maxFileBytes {
			addWarning("Large files were excluded from project analysis.")
			return nil
		}
		if totalBytes+int64(len(data)) > maxTotalBytes {
			addWarning("Project analysis input was truncated to stay within safe size limits.")
			return fs.SkipAll
		}
		if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
			addWarning("Binary and non-text files were excluded from project analysis.")
			return nil
		}
		payload.Files = append(payload.Files, ProjectContextFile{Path: rel, Content: string(data)})
		totalBytes += int64(len(data))
		return nil
	})
	if err != nil {
		return ProjectContextPayload{}, fmt.Errorf("collect project context: %w", err)
	}
	if len(payload.Files) == 0 {
		return ProjectContextPayload{}, errors.New("project directory contains no readable text files")
	}
	sort.Slice(payload.Files, func(i, j int) bool { return payload.Files[i].Path < payload.Files[j].Path })
	for warning := range warnings {
		payload.Warnings = append(payload.Warnings, warning)
	}
	sort.Strings(payload.Warnings)
	return payload, nil
}

func SanitizeProjectContext(input ProjectContextPayload) (ProjectContextPayload, error) {
	if len(input.Files) == 0 {
		return ProjectContextPayload{}, errors.New("project_context.files must contain at least one file")
	}
	rootName := strings.TrimSpace(input.RootName)
	if len(rootName) > 200 {
		return ProjectContextPayload{}, errors.New("project_context.root_name exceeds 200 characters")
	}
	clean := ProjectContextPayload{
		RootName:  rootName,
		Files:     []ProjectContextFile{},
		LocalPath: strings.TrimSpace(input.LocalPath),
		DaemonID:  strings.TrimSpace(input.DaemonID),
	}
	if len(clean.LocalPath) > 4096 || strings.IndexByte(clean.LocalPath, 0) >= 0 {
		return ProjectContextPayload{}, errors.New("project_context.local_path is invalid")
	}
	portableLocalPath := strings.ReplaceAll(clean.LocalPath, "\\", "/")
	if clean.LocalPath != "" && !strings.HasPrefix(portableLocalPath, "/") && !windowsDrivePath.MatchString(portableLocalPath) {
		return ProjectContextPayload{}, errors.New("project_context.local_path must be an absolute path")
	}
	if len(clean.DaemonID) > 200 || strings.IndexByte(clean.DaemonID, 0) >= 0 {
		return ProjectContextPayload{}, errors.New("project_context.daemon_id is invalid")
	}
	seen := map[string]bool{}
	var total int
	for _, file := range input.Files {
		path := strings.ReplaceAll(strings.TrimSpace(file.Path), "\\", "/")
		if path == "" || strings.HasPrefix(path, "/") || windowsDrivePath.MatchString(path) || filepath.ToSlash(filepath.Clean(path)) != path || path == ".." || strings.HasPrefix(path, "../") {
			return ProjectContextPayload{}, fmt.Errorf("project_context contains an invalid file path %q", file.Path)
		}
		if seen[path] {
			return ProjectContextPayload{}, fmt.Errorf("project_context contains duplicate file path %q", path)
		}
		seen[path] = true
		if containsExcludedDirectory(path) || sensitiveProjectFile.MatchString(path) {
			clean.Warnings = append(clean.Warnings, "Sensitive and generated files were excluded from project analysis.")
			continue
		}
		content := []byte(file.Content)
		if !utf8.Valid(content) || strings.IndexByte(file.Content, 0) >= 0 {
			continue
		}
		if len(content) > maxDiscoveryFileBytes {
			continue
		}
		if len(clean.Files) >= maxDiscoveryFiles || total+len(content) > maxDiscoveryTotalBytes {
			clean.Warnings = append(clean.Warnings, "Project analysis input was truncated to stay within safe size limits.")
			break
		}
		clean.Files = append(clean.Files, ProjectContextFile{Path: path, Content: file.Content})
		total += len(content)
	}
	if len(clean.Files) == 0 {
		return ProjectContextPayload{}, errors.New("project_context contains no readable project files")
	}
	for _, warning := range input.Warnings {
		warning = strings.TrimSpace(warning)
		if warning != "" && len(warning) <= 300 {
			clean.Warnings = append(clean.Warnings, warning)
		}
	}
	clean.Warnings = uniqueStrings(clean.Warnings)
	sort.Slice(clean.Files, func(i, j int) bool { return clean.Files[i].Path < clean.Files[j].Path })
	return clean, nil
}

func containsExcludedDirectory(path string) bool {
	parts := strings.Split(path, "/")
	for _, part := range parts[:len(parts)-1] {
		if excludedProjectDirectories[strings.ToLower(part)] {
			return true
		}
	}
	return false
}

func ValidateDiscoveryReport(report ProjectDiscoveryReport) error {
	if err := requiredDiscoveryText("project.title", report.Project.Title, 200); err != nil {
		return err
	}
	if err := requiredDiscoveryText("project.purpose", report.Project.Purpose, 20000); err != nil {
		return err
	}
	if err := requiredDiscoveryText("project.goal", report.Project.Goal, 20000); err != nil {
		return err
	}
	if err := requiredDiscoveryText("project.architecture_summary", report.Project.ArchitectureSummary, 20000); err != nil {
		return err
	}
	for name, values := range map[string][]string{
		"boundaries.in_scope":           report.Boundaries.InScope,
		"boundaries.out_of_scope":       report.Boundaries.OutOfScope,
		"boundaries.constraints":        report.Boundaries.Constraints,
		"boundaries.definition_of_done": report.Boundaries.DefinitionOfDone,
	} {
		if err := validateDiscoveryList(name, values, 50); err != nil {
			return err
		}
	}
	if len(report.ImplementedFeatures) > maxDiscoveryReportItems || len(report.NextTasks) > maxDiscoveryReportItems {
		return fmt.Errorf("discovery report supports at most %d features and tasks each", maxDiscoveryReportItems)
	}
	keys := make(map[string]bool, len(report.ImplementedFeatures)+len(report.NextTasks))
	for index, feature := range report.ImplementedFeatures {
		if err := validateDiscoveryItem(fmt.Sprintf("implemented_features[%d]", index), feature.Key, feature.Title, feature.Description); err != nil {
			return err
		}
		if keys[feature.Key] {
			return fmt.Errorf("discovery report contains duplicate key %q", feature.Key)
		}
		keys[feature.Key] = true
		if err := validateDiscoveryList(fmt.Sprintf("implemented_features[%d].evidence", index), feature.Evidence, 20); err != nil {
			return err
		}
	}
	taskKeys := make(map[string]bool, len(report.NextTasks))
	for index, task := range report.NextTasks {
		if err := validateDiscoveryItem(fmt.Sprintf("next_tasks[%d]", index), task.Key, task.Title, task.Description); err != nil {
			return err
		}
		if keys[task.Key] {
			return fmt.Errorf("discovery report contains duplicate key %q", task.Key)
		}
		keys[task.Key], taskKeys[task.Key] = true, true
		if task.Stage < 1 || task.Stage > 100 {
			return fmt.Errorf("next_tasks[%d].stage must be between 1 and 100", index)
		}
		switch task.Priority {
		case "", "urgent", "high", "medium", "low", "none":
		default:
			return fmt.Errorf("next_tasks[%d].priority is invalid", index)
		}
		if err := validateDiscoveryList(fmt.Sprintf("next_tasks[%d].acceptance_criteria", index), task.AcceptanceCriteria, 20); err != nil {
			return err
		}
	}
	for index, task := range report.NextTasks {
		if task.ParentKey != "" && !taskKeys[task.ParentKey] {
			return fmt.Errorf("next_tasks[%d].parent_key references unknown task %q", index, task.ParentKey)
		}
		seen := map[string]bool{}
		for _, dependency := range task.BlockedByKeys {
			if !keys[dependency] || dependency == task.Key || seen[dependency] {
				return fmt.Errorf("next_tasks[%d] has invalid dependency %q", index, dependency)
			}
			seen[dependency] = true
		}
	}
	return nil
}

func BuildBundleFromDiscovery(report ProjectDiscoveryReport, context *ProjectContextPayload) (Bundle, error) {
	if err := ValidateDiscoveryReport(report); err != nil {
		return Bundle{}, err
	}
	features := make([]PortableTask, 0, len(report.ImplementedFeatures)+len(report.NextTasks))
	nodes := make([]PlanNode, 0, len(report.ImplementedFeatures)+len(report.NextTasks))
	for _, feature := range report.ImplementedFeatures {
		// Issues only accept stage >= 1; completed discoveries remain unstaged.
		features = append(features, PortableTask{Key: feature.Key, OriginID: "discovery:" + feature.Key, Title: feature.Title,
			Description: stringPointer(feature.Description), Status: stringPointer("done"),
			Labels: []PortableLabelRef{{Name: "implemented"}}, AcceptanceCriteria: []string{},
		})
		nodes = append(nodes, PlanNode{Key: feature.Key, Title: feature.Title, Description: feature.Description, Kind: "feature", Status: "completed", AcceptanceCriteria: []string{}})
	}
	for _, task := range report.NextTasks {
		status, priority := "todo", task.Priority
		if priority == "" {
			priority = "medium"
		}
		portableTask := PortableTask{Key: task.Key, OriginID: "discovery:" + task.Key, Title: task.Title,
			Description: stringPointer(task.Description), Status: &status, Priority: &priority,
			Stage: int32Pointer(task.Stage), BlockedByKeys: append([]string{}, task.BlockedByKeys...),
			AcceptanceCriteria: append([]string{}, task.AcceptanceCriteria...),
		}
		if task.ParentKey != "" {
			portableTask.ParentKey = stringPointer(task.ParentKey)
		}
		features = append(features, portableTask)
		nodes = append(nodes, PlanNode{Key: task.Key, Title: task.Title, Description: task.Description, Kind: "implementation", Status: "pending", AcceptanceCriteria: append([]string{}, task.AcceptanceCriteria...)})
	}
	planEdges := []PlanEdge{}
	for _, task := range report.NextTasks {
		for _, dependency := range task.BlockedByKeys {
			planEdges = append(planEdges, PlanEdge{From: dependency, To: task.Key, Type: "hard"})
		}
	}
	requirements := make([]PortableSpecificationRequirement, 0, len(report.Boundaries.InScope)+len(report.Boundaries.DefinitionOfDone))
	for index, text := range append(append([]string{}, report.Boundaries.InScope...), report.Boundaries.DefinitionOfDone...) {
		if strings.TrimSpace(text) == "" {
			continue
		}
		source := "in_scope"
		if index >= len(report.Boundaries.InScope) {
			source = "definition_of_done"
		}
		requirements = append(requirements, PortableSpecificationRequirement{ID: fmt.Sprintf("discovery-%03d", index+1), Text: text, Rationale: "Identified during static project discovery.", Source: source})
	}
	if len(requirements) == 0 {
		requirements = append(requirements, PortableSpecificationRequirement{ID: "discovery-001", Text: report.Project.Goal, Rationale: "Preserves the discovered project goal.", Source: "project_goal"})
	}
	spec := &PortableSpecification{
		Goal: report.Project.Goal, Summary: report.Project.Purpose + "\n\n" + report.Project.ArchitectureSummary,
		Requirements: requirements, NonFunctionalRequirements: []string{},
		Constraints: append([]string{}, report.Boundaries.Constraints...), NonGoals: append([]string{}, report.Boundaries.OutOfScope...),
		DefinitionOfDone: append([]string{}, report.Boundaries.DefinitionOfDone...), AcceptanceExpectations: []string{},
	}
	knowledge := []PortableKnowledge{
		knowledgeJSON("requirement", "Project purpose", map[string]any{"purpose": report.Project.Purpose, "goal": report.Project.Goal}),
		knowledgeJSON("architecture", "Architecture summary", map[string]any{"summary": report.Project.ArchitectureSummary}),
		knowledgeJSON("requirement", "Project boundaries", report.Boundaries),
		knowledgeJSON("architecture", "Implemented features", report.ImplementedFeatures),
	}
	resources := []PortableResource{}
	contextWarnings := []string{}
	sourceAttached := false
	if context != nil && context.LocalPath != "" && context.DaemonID != "" {
		ref, err := json.Marshal(map[string]string{
			"local_path": context.LocalPath, "daemon_id": context.DaemonID, "execution_mode": "in_place",
		})
		if err != nil {
			return Bundle{}, fmt.Errorf("encode local project resource: %w", err)
		}
		resources = append(resources, PortableResource{ResourceType: "local_directory", ResourceRef: ref})
		sourceAttached = true
	}
	if !sourceAttached {
		knowledge = append(knowledge, knowledgeJSON("reference", "Source access", map[string]any{"available": false, "message": "The selected files were analyzed, but no agent-accessible local directory or Git source is attached. Attach a source before asking agents to edit files."}))
		contextWarnings = append(contextWarnings, "The selected files were used for analysis only. No agent-accessible local directory or Git source is attached; connect a source before agents edit files.")
	}
	if context != nil {
		contextWarnings = append(contextWarnings, context.Warnings...)
	}
	description := report.Project.Purpose
	brief := report.Project.Goal + "\n\n" + report.Project.ArchitectureSummary
	bundle := Bundle{SchemaVersion: SchemaVersionV1, Project: PortableProject{Title: report.Project.Title, Description: &description},
		Resources: resources, Bootstrap: &PortableBootstrap{AutonomyMode: "standard", AutonomyLevel: "development", Brief: brief, Knowledge: knowledge,
			Plan: &PortablePlan{Goal: report.Project.Goal, Requirements: portablePlanRequirements(requirements), Specification: spec, Nodes: nodes, Edges: planEdges}}, Tasks: features}
	for _, warning := range uniqueStrings(contextWarnings) {
		bundle.Bootstrap.Knowledge = append(bundle.Bootstrap.Knowledge, knowledgeJSON("fact", "Import warning", map[string]string{"message": warning}))
	}
	if err := ValidateBundleForImport(bundle); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func requiredDiscoveryText(name, value string, maxLength int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) > maxLength {
		return fmt.Errorf("%s exceeds %d characters", name, maxLength)
	}
	return nil
}

func validateDiscoveryItem(name, key, title, description string) error {
	if err := requiredDiscoveryText(name+".key", key, 120); err != nil {
		return err
	}
	if strings.ContainsAny(key, "\r\n\t ") {
		return fmt.Errorf("%s.key must not contain whitespace", name)
	}
	if err := requiredDiscoveryText(name+".title", title, 200); err != nil {
		return err
	}
	if len(description) > 10000 {
		return fmt.Errorf("%s.description exceeds 10000 characters", name)
	}
	return nil
}

func validateDiscoveryList(name string, values []string, max int) error {
	if len(values) > max {
		return fmt.Errorf("%s supports at most %d items", name, max)
	}
	for index, value := range values {
		if strings.TrimSpace(value) == "" || len(value) > 2000 {
			return fmt.Errorf("%s[%d] must contain 1 to 2000 characters", name, index)
		}
	}
	return nil
}

func knowledgeJSON(kind, title string, value any) PortableKnowledge {
	content, _ := json.Marshal(value)
	return PortableKnowledge{Kind: kind, Title: title, Content: content}
}

func portablePlanRequirements(values []PortableSpecificationRequirement) []PlanRequirement {
	result := make([]PlanRequirement, 0, len(values))
	for _, value := range values {
		result = append(result, PlanRequirement{ID: value.ID, Text: value.Text, Rationale: value.Rationale})
	}
	return result
}

func stringPointer(value string) *string { return &value }
func int32Pointer(value int32) *int32    { return &value }

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
