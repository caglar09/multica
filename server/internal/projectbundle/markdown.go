package projectbundle

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

func RenderTasksMarkdown(bundle Bundle) string {
	var out strings.Builder
	title := strings.TrimSpace(bundle.Project.Title)
	if title == "" {
		title = "Project tasks"
	}
	fmt.Fprintf(&out, "# %s\n\n## Tasks\n", title)

	children := make(map[string][]PortableTask)
	for _, task := range bundle.Tasks {
		parent := ""
		if task.ParentKey != nil {
			parent = *task.ParentKey
		}
		children[parent] = append(children[parent], task)
	}
	for parent := range children {
		sort.SliceStable(children[parent], func(i, j int) bool {
			a, b := children[parent][i], children[parent][j]
			if a.Position != nil && b.Position != nil && *a.Position != *b.Position {
				return *a.Position < *b.Position
			}
			return a.Key < b.Key
		})
	}
	var render func(string, int)
	render = func(parent string, depth int) {
		for _, task := range children[parent] {
			checkbox := " "
			if taskDone(task) {
				checkbox = "x"
			}
			indent := strings.Repeat("  ", depth)
			fmt.Fprintf(&out, "%s- [%s] %s <!-- multica:key=%s", indent, checkbox, cleanMarkdownTitle(task.Title), url.QueryEscape(task.Key))
			if task.OriginID != "" {
				fmt.Fprintf(&out, " origin_id=%s", url.QueryEscape(task.OriginID))
			}
			out.WriteString(" -->\n")
			if task.Description != nil && strings.TrimSpace(*task.Description) != "" {
				fmt.Fprintf(&out, "%s  - Description: %s\n", indent, strings.ReplaceAll(strings.TrimSpace(*task.Description), "\n", " "))
			}
			if len(task.Labels) > 0 {
				labels := make([]string, 0, len(task.Labels))
				for _, label := range task.Labels {
					labels = append(labels, label.Name)
				}
				sort.Strings(labels)
				fmt.Fprintf(&out, "%s  - Labels: %s\n", indent, strings.Join(labels, ", "))
			}
			if len(task.BlockedByKeys) > 0 {
				fmt.Fprintf(&out, "%s  - Blocked by: %s\n", indent, strings.Join(task.BlockedByKeys, ", "))
			}
			for _, criterion := range task.AcceptanceCriteria {
				fmt.Fprintf(&out, "%s  - Acceptance: %s\n", indent, strings.ReplaceAll(strings.TrimSpace(criterion), "\n", " "))
			}
			render(task.Key, depth+1)
		}
	}
	render("", 0)
	return out.String()
}

func RenderAgentGuideMarkdown(bundle Bundle) string {
	return fmt.Sprintf("# Working with %s\n\nThis project was exported from Multica. Use `.multica/project.json` as the complete project context and `TASKS.md` as the editable task checklist.\n\n- Mark a task complete by changing its checkbox to `[x]`.\n- Add new work as a checklist item. Indent it under a task when it is a subtask.\n- Keep the `multica:key` marker on existing tasks so sync can match them.\n- Do not remove tasks to indicate completion; unchecked and checked tasks both remain in Multica.\n- Attachments are not imported by portability sync.\n\nReview `TASKS.md` before syncing. Use `multica project sync` to preview changes with `--dry-run`, then apply them.\n", markdownHeading(bundle.Project.Title))
}

func ParseMarkdownToBundle(projectTitleHint, markdownContent string) (Bundle, error) {
	bundle := Bundle{
		SchemaVersion: SchemaVersionV1,
		Project:       PortableProject{},
		Tasks:         []PortableTask{},
	}
	type stackEntry struct {
		indent int
		key    string
	}
	var stack []stackEntry
	siblingCount := make(map[string]int)
	scanner := bufio.NewScanner(strings.NewReader(markdownContent))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") && bundle.Project.Title == "" {
			bundle.Project.Title = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			bundle.Project.Title = strings.TrimPrefix(bundle.Project.Title, "Project: ")
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		content := strings.TrimLeft(line, " \t")
		if !isTaskListLine(content) {
			continue
		}
		checked := content[3] == 'x' || content[3] == 'X'
		title, markers := parseTaskMarkers(strings.TrimSpace(content[5:]))
		if title == "" {
			return Bundle{}, fmt.Errorf("line %d: task title is required", lineNumber)
		}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parent := ""
		if len(stack) > 0 {
			parent = stack[len(stack)-1].key
		}
		key := markers["key"]
		if key == "" {
			ordinal := siblingCount[parent]
			siblingCount[parent]++
			seed := fmt.Sprintf("%s\x00%s\x00%d", parent, strings.ToLower(strings.Join(strings.Fields(title), " ")), ordinal)
			digest := sha256.Sum256([]byte(seed))
			key = "new-" + hex.EncodeToString(digest[:6])
		}
		var parentKey *string
		if markerParent := markers["parent_key"]; markerParent != "" {
			parentKey = &markerParent
		} else if parent != "" {
			parentKey = &parent
		}
		status, category := "todo", "unstarted"
		if checked {
			status, category = "done", "done"
		}
		position := float64(len(bundle.Tasks))
		task := PortableTask{
			Key:             key,
			OriginID:        markers["origin_id"],
			Title:           title,
			Status:          &status,
			StatusCategory:  &category,
			ParentKey:       parentKey,
			Position:        &position,
			markdownChecked: &checked,
			present:         map[string]bool{"key": true, "title": true, "status": true},
		}
		bundle.Tasks = append(bundle.Tasks, task)
		stack = append(stack, stackEntry{indent: indent, key: key})
	}
	if err := scanner.Err(); err != nil {
		return Bundle{}, err
	}
	if bundle.Project.Title == "" {
		bundle.Project.Title = strings.TrimSpace(projectTitleHint)
		if bundle.Project.Title == "" {
			bundle.Project.Title = "Imported project"
		}
	}
	if err := ValidateBundle(bundle); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func ReconcileBundleWithMarkdown(base Bundle, markdownContent string) (Bundle, error) {
	if err := ValidateBundle(base); err != nil {
		return Bundle{}, fmt.Errorf("base bundle: %w", err)
	}
	parsed, err := ParseMarkdownToBundle(base.Project.Title, markdownContent)
	if err != nil {
		return Bundle{}, err
	}
	base.Project.present = map[string]bool{}
	for i := range base.Tasks {
		base.Tasks[i].present = map[string]bool{"key": true}
	}
	indexes := make(map[string]int, len(base.Tasks))
	for i, task := range base.Tasks {
		indexes[task.Key] = i
	}
	for _, task := range parsed.Tasks {
		if index, exists := indexes[task.Key]; exists {
			if task.markdownChecked != nil && *task.markdownChecked != taskDone(base.Tasks[index]) {
				if *task.markdownChecked {
					status, category := "done", "done"
					base.Tasks[index].Status = &status
					base.Tasks[index].StatusCategory = &category
				} else {
					status, category := "todo", "unstarted"
					base.Tasks[index].Status = &status
					base.Tasks[index].StatusCategory = &category
				}
				if base.Tasks[index].present == nil {
					base.Tasks[index].present = map[string]bool{}
				}
				base.Tasks[index].present["status"] = true
				base.Tasks[index].present["status_category"] = true
			}
			continue
		}
		base.Tasks = append(base.Tasks, task)
		indexes[task.Key] = len(base.Tasks) - 1
	}
	if err := ValidateBundle(base); err != nil {
		return Bundle{}, err
	}
	return base, nil
}

func isTaskListLine(line string) bool {
	return len(line) >= 6 && (strings.HasPrefix(line, "- [") || strings.HasPrefix(line, "* [") || strings.HasPrefix(line, "+ [")) &&
		(line[3] == ' ' || line[3] == 'x' || line[3] == 'X') && line[4] == ']' && line[5] == ' '
}

func parseTaskMarkers(raw string) (string, map[string]string) {
	markers := make(map[string]string)
	title := raw
	for {
		start := strings.Index(title, "<!--")
		if start < 0 {
			break
		}
		endRel := strings.Index(title[start+4:], "-->")
		if endRel < 0 {
			break
		}
		end := start + 4 + endRel
		marker := strings.TrimSpace(title[start+4 : end])
		if strings.HasPrefix(marker, "multica:") {
			for _, part := range strings.Fields(strings.TrimPrefix(marker, "multica:")) {
				key, value, ok := strings.Cut(part, "=")
				if !ok {
					continue
				}
				if decoded, err := url.QueryUnescape(value); err == nil {
					markers[key] = decoded
				}
			}
		}
		title = title[:start] + title[end+3:]
	}
	return cleanMarkdownTitle(strings.TrimSpace(title)), markers
}

func taskDone(task PortableTask) bool {
	if task.StatusCategory != nil {
		return *task.StatusCategory == "done" || *task.StatusCategory == "closed"
	}
	if task.Status == nil {
		return false
	}
	return *task.Status == "done" || *task.Status == "cancelled"
}

func cleanMarkdownTitle(title string) string {
	return strings.TrimSpace(strings.ReplaceAll(title, "\r", ""))
}

func markdownHeading(title string) string {
	title = cleanMarkdownTitle(title)
	if title == "" {
		return "project"
	}
	return title
}
