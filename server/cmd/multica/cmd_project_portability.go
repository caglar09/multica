package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/projectbundle"
)

var projectExportCmd = &cobra.Command{
	Use:   "export <id>",
	Short: "Export a project bundle",
	Args:  exactArgs(1),
	RunE:  runProjectExport,
}

var projectImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Import a project bundle or Markdown task list",
	Args:  exactArgs(0),
	RunE:  runProjectImport,
}

var projectSyncCmd = &cobra.Command{
	Use:   "sync [id]",
	Short: "Sync a project from a bundle or Markdown task list",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runProjectSync,
}

type projectExportResponse struct {
	Bundle             json.RawMessage `json:"bundle"`
	TasksMarkdown      string          `json:"tasks_markdown"`
	AgentGuideMarkdown string          `json:"agent_guide_markdown"`
}

type projectPortabilityInput struct {
	Bundle         *projectbundle.Bundle
	Markdown       string
	ProjectContext *projectbundle.ProjectContextPayload
}

func runProjectExport(cmd *cobra.Command, args []string) error {
	filePath, _ := cmd.Flags().GetString("file")
	dirPath, _ := cmd.Flags().GetString("dir")
	output, _ := cmd.Flags().GetString("output")
	if err := validateExportDestination(cmd, filePath, dirPath, output); err != nil {
		return err
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	projectRef, err := resolveProjectID(ctx, client, args[0])
	if err != nil {
		return fmt.Errorf("resolve project: %w", err)
	}
	var response projectExportResponse
	path := "/api/projects/" + url.PathEscape(projectRef.ID) + "/export"
	if err := client.GetJSON(ctx, path, &response); err != nil {
		return fmt.Errorf("export project: %w", err)
	}
	bundle, err := validateExportResponse(response)
	if err != nil {
		return err
	}

	switch {
	case dirPath != "":
		return writeProjectExportDirectory(dirPath, bundle, response)
	case filePath != "":
		return writeProjectBundleFile(filePath, bundle)
	case output == "markdown":
		_, err := io.WriteString(os.Stdout, response.TasksMarkdown)
		return err
	default:
		return cli.PrintJSON(os.Stdout, bundle)
	}
}

func runProjectImport(cmd *cobra.Command, _ []string) error {
	filePath, _ := cmd.Flags().GetString("file")
	dirPath, _ := cmd.Flags().GetString("dir")
	title, _ := cmd.Flags().GetString("title")
	goal, _ := cmd.Flags().GetString("goal")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	output, _ := cmd.Flags().GetString("output")
	if err := validatePortabilityOutput(output); err != nil {
		return err
	}
	title = strings.TrimSpace(title)
	if cmd.Flags().Changed("title") && title == "" {
		return fmt.Errorf("--title must not be empty")
	}

	input, err := readProjectPortabilityInput(filePath, dirPath, cmd.Flags().Changed("file"), cmd.Flags().Changed("dir"), title)
	if err != nil {
		return fmt.Errorf("read project input: %w", err)
	}
	body := projectImportRequest(input, dryRun, title)
	body.Instruction = strings.TrimSpace(goal)

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var result projectbundle.PortabilityResult
	if input.ProjectContext != nil && !dryRun {
		previewRequest := body
		previewRequest.DryRun = true
		if err := client.PostJSON(ctx, "/api/projects/import", previewRequest, &result); err != nil {
			return fmt.Errorf("preview project import: %w", err)
		}
		printProjectDiscoveryPreview(cmd.ErrOrStderr(), result)
		if !confirmProjectDiscoveryImport(cmd) {
			return nil
		}
		if result.DiscoveryReport == nil {
			return fmt.Errorf("project discovery preview did not return a report; refusing to import")
		}
		body.ProjectContext = nil
		body.DiscoveryReport = result.DiscoveryReport
	}
	if err := client.PostJSON(ctx, "/api/projects/import", body, &result); err != nil {
		return fmt.Errorf("import project: %w", err)
	}
	addSkippedAttachmentWarning(&result, input.Bundle)
	return printProjectPortabilityResult(output, result)
}

func runProjectSync(cmd *cobra.Command, args []string) error {
	filePath, _ := cmd.Flags().GetString("file")
	dirPath, _ := cmd.Flags().GetString("dir")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	output, _ := cmd.Flags().GetString("output")
	if err := validatePortabilityOutput(output); err != nil {
		return err
	}

	input, err := readProjectPortabilityInput(filePath, dirPath, cmd.Flags().Changed("file"), cmd.Flags().Changed("dir"), "")
	if err != nil {
		return fmt.Errorf("read project input: %w", err)
	}
	if input.ProjectContext != nil {
		return fmt.Errorf("raw project directories can be imported, but cannot be synced")
	}
	projectID := ""
	if len(args) == 1 {
		projectID = args[0]
	} else if input.Bundle != nil {
		projectID = input.Bundle.Origin.ProjectID
	}
	if strings.TrimSpace(projectID) == "" {
		return fmt.Errorf("project id is required when the input has no origin.project_id")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	projectRef, err := resolveProjectID(ctx, client, projectID)
	if err != nil {
		return fmt.Errorf("resolve project: %w", err)
	}

	var result projectbundle.PortabilityResult
	path := "/api/projects/" + url.PathEscape(projectRef.ID) + "/sync"
	if err := client.PostJSON(ctx, path, projectSyncRequest(input, dryRun), &result); err != nil {
		return fmt.Errorf("sync project: %w", err)
	}
	addSkippedAttachmentWarning(&result, input.Bundle)
	return printProjectPortabilityResult(output, result)
}

func addSkippedAttachmentWarning(result *projectbundle.PortabilityResult, bundle *projectbundle.Bundle) {
	if bundle == nil || !bundle.HasUnsupportedAttachments() {
		return
	}
	const warning = "Attachments are unsupported by project portability and were skipped."
	for _, existing := range result.Warnings {
		if existing == warning {
			return
		}
	}
	result.Warnings = append(result.Warnings, warning)
}

func validateExportDestination(cmd *cobra.Command, filePath, dirPath, output string) error {
	if filePath != "" && dirPath != "" {
		return fmt.Errorf("--file and --dir cannot be used together")
	}
	if cmd.Flags().Changed("file") && strings.TrimSpace(filePath) == "" {
		return fmt.Errorf("--file must not be empty")
	}
	if cmd.Flags().Changed("dir") && strings.TrimSpace(dirPath) == "" {
		return fmt.Errorf("--dir must not be empty")
	}
	if (filePath != "" || dirPath != "") && cmd.Flags().Changed("output") {
		return fmt.Errorf("--output cannot be used with --file or --dir")
	}
	if output != "json" && output != "markdown" {
		return fmt.Errorf("invalid --output %q; valid values: json, markdown", output)
	}
	return nil
}

func validatePortabilityOutput(output string) error {
	if output != "table" && output != "json" {
		return fmt.Errorf("invalid --output %q; valid values: table, json", output)
	}
	return nil
}

func validateExportResponse(response projectExportResponse) (projectbundle.Bundle, error) {
	bundle, err := projectbundle.DecodeBundle(response.Bundle)
	if err != nil {
		return projectbundle.Bundle{}, fmt.Errorf("invalid export response bundle: %w", err)
	}
	if strings.TrimSpace(response.TasksMarkdown) == "" || strings.TrimSpace(response.AgentGuideMarkdown) == "" {
		return projectbundle.Bundle{}, fmt.Errorf("export response is missing Markdown content")
	}
	return bundle, nil
}

func readProjectPortabilityInput(filePath, dirPath string, fileSet, dirSet bool, titleHint string) (projectPortabilityInput, error) {
	if fileSet && dirSet || filePath != "" && dirPath != "" {
		return projectPortabilityInput{}, fmt.Errorf("--file and --dir cannot be used together")
	}
	if fileSet && strings.TrimSpace(filePath) == "" || dirSet && strings.TrimSpace(dirPath) == "" {
		return projectPortabilityInput{}, fmt.Errorf("input path must not be empty")
	}
	if filePath != "" || fileSet {
		info, err := os.Stat(filePath)
		if err != nil {
			return projectPortabilityInput{}, err
		}
		if info.IsDir() {
			return projectPortabilityInput{}, fmt.Errorf("--file must point to a bundle or Markdown file")
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return projectPortabilityInput{}, err
		}
		return parseProjectPortabilityFile(filePath, data, titleHint)
	}

	if dirPath == "" {
		dirPath = "."
	}
	info, err := os.Stat(dirPath)
	if err != nil {
		return projectPortabilityInput{}, err
	}
	if !info.IsDir() {
		return projectPortabilityInput{}, fmt.Errorf("--dir must point to a project directory")
	}

	bundlePath := filepath.Join(dirPath, ".multica", "project.json")
	tasksPath := filepath.Join(dirPath, "TASKS.md")
	bundleData, bundleErr := os.ReadFile(bundlePath)
	tasksData, tasksErr := os.ReadFile(tasksPath)
	tasksMarkdown := ""
	if bundleErr != nil && !os.IsNotExist(bundleErr) {
		return projectPortabilityInput{}, bundleErr
	}
	if tasksErr != nil && !os.IsNotExist(tasksErr) {
		return projectPortabilityInput{}, tasksErr
	}
	if bundleErr != nil && tasksErr != nil {
		context, err := projectbundle.CollectProjectContext(dirPath, projectbundle.CollectOptions{})
		if err != nil {
			return projectPortabilityInput{}, err
		}
		return projectPortabilityInput{ProjectContext: &context}, nil
	}
	if tasksErr == nil {
		if !utf8.Valid(tasksData) {
			return projectPortabilityInput{}, fmt.Errorf("%s is not valid UTF-8", tasksPath)
		}
		tasksMarkdown = strings.TrimPrefix(string(tasksData), "\ufeff")
		if strings.TrimSpace(tasksMarkdown) == "" {
			return projectPortabilityInput{}, fmt.Errorf("%s is empty", tasksPath)
		}
	}
	if bundleErr == nil {
		bundle, err := decodeProjectBundleFile(bundlePath, bundleData)
		if err != nil {
			return projectPortabilityInput{}, err
		}
		if tasksErr == nil {
			bundle, err = projectbundle.ReconcileBundleWithMarkdown(bundle, tasksMarkdown)
			if err != nil {
				return projectPortabilityInput{}, fmt.Errorf("reconcile %s: %w", tasksPath, err)
			}
		}
		return projectPortabilityInput{Bundle: &bundle}, nil
	}
	return parseProjectPortabilityFile(tasksPath, tasksData, titleHint)
}

func parseProjectPortabilityFile(path string, data []byte, titleHint string) (projectPortabilityInput, error) {
	if !utf8.Valid(data) {
		return projectPortabilityInput{}, fmt.Errorf("%s is not valid UTF-8", path)
	}
	data = []byte(strings.TrimPrefix(string(data), "\ufeff"))
	ext := strings.ToLower(filepath.Ext(path))
	trimmed := strings.TrimSpace(string(data))
	if ext == ".json" || ext != ".md" && (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) {
		bundle, err := decodeProjectBundleFile(path, data)
		if err != nil {
			return projectPortabilityInput{}, err
		}
		return projectPortabilityInput{Bundle: &bundle}, nil
	}
	if trimmed == "" {
		return projectPortabilityInput{}, fmt.Errorf("%s is empty", path)
	}
	if _, err := projectbundle.ParseMarkdownToBundle(titleHint, string(data)); err != nil {
		return projectPortabilityInput{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return projectPortabilityInput{Markdown: string(data)}, nil
}

func decodeProjectBundleFile(path string, data []byte) (projectbundle.Bundle, error) {
	if !utf8.Valid(data) {
		return projectbundle.Bundle{}, fmt.Errorf("%s is not valid UTF-8", path)
	}
	data = []byte(strings.TrimPrefix(string(data), "\ufeff"))
	bundle, err := projectbundle.DecodeBundle(data)
	if err != nil {
		return projectbundle.Bundle{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return bundle, nil
}

func projectImportRequest(input projectPortabilityInput, dryRun bool, titleHint string) projectbundle.ImportRequest {
	request := projectbundle.ImportRequest{DryRun: dryRun, ProjectTitleHint: titleHint}
	if input.Bundle != nil {
		request.Bundle = input.Bundle
	} else if input.ProjectContext != nil {
		request.ProjectContext = input.ProjectContext
	} else {
		request.Markdown = input.Markdown
	}
	return request
}

func printProjectDiscoveryPreview(w io.Writer, result projectbundle.PortabilityResult) {
	if result.DiscoveryReport == nil {
		fmt.Fprintln(w, "Project discovery did not return a preview report.")
		return
	}
	report := result.DiscoveryReport
	fmt.Fprintf(w, "Project: %s\nPurpose: %s\nGoal: %s\n\n", report.Project.Title, report.Project.Purpose, report.Project.Goal)
	fmt.Fprintln(w, "Next tasks:")
	for _, task := range report.NextTasks {
		fmt.Fprintf(w, "  - %s\n", task.Title)
	}
	if result.Preview != nil && len(result.Preview.Warnings) > 0 {
		fmt.Fprintln(w, "Warnings:")
		for _, warning := range result.Preview.Warnings {
			fmt.Fprintf(w, "  - %s\n", warning)
		}
	}
}

func confirmProjectDiscoveryImport(cmd *cobra.Command) bool {
	fmt.Fprint(cmd.ErrOrStderr(), "Import this reviewed project into Multica? [y/N] ")
	answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	return err == nil && strings.EqualFold(strings.TrimSpace(answer), "y")
}

func projectSyncRequest(input projectPortabilityInput, dryRun bool) projectbundle.SyncRequest {
	request := projectbundle.SyncRequest{DryRun: dryRun}
	if input.Bundle != nil {
		request.Bundle = input.Bundle
	} else {
		request.Markdown = input.Markdown
	}
	return request
}

func writeProjectBundleFile(path string, bundle projectbundle.Bundle) error {
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("encode project bundle: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func writeProjectExportDirectory(path string, bundle projectbundle.Bundle, response projectExportResponse) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create export directory: %w", err)
	}
	bundlePath := filepath.Join(path, ".multica", "project.json")
	if err := os.MkdirAll(filepath.Dir(bundlePath), 0o755); err != nil {
		return fmt.Errorf("create project metadata directory: %w", err)
	}
	if err := writeProjectBundleFile(bundlePath, bundle); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(path, "TASKS.md"), []byte(response.TasksMarkdown), 0o644); err != nil {
		return fmt.Errorf("write TASKS.md: %w", err)
	}
	if err := os.WriteFile(filepath.Join(path, ".multica", "AGENT_GUIDE.md"), []byte(response.AgentGuideMarkdown), 0o644); err != nil {
		return fmt.Errorf("write .multica/AGENT_GUIDE.md: %w", err)
	}
	return nil
}

func printProjectPortabilityResult(output string, result projectbundle.PortabilityResult) error {
	if output == "json" {
		return cli.PrintJSON(os.Stdout, result)
	}
	cli.PrintTable(os.Stdout,
		[]string{"PROJECT", "CREATED", "UPDATED", "UNCHANGED", "WARNINGS"},
		[][]string{{
			result.ProjectID,
			fmt.Sprint(result.CreatedCount),
			fmt.Sprint(result.UpdatedCount),
			fmt.Sprint(result.UnchangedCount),
			strings.Join(result.Warnings, "; "),
		}},
	)
	return nil
}
