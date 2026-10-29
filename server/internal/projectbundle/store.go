package projectbundle

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/entitlement"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var ErrProjectNotFound = errors.New("project not found in workspace")

var validProjectStatuses = map[string]bool{"planned": true, "in_progress": true, "paused": true, "completed": true, "cancelled": true}
var validPriorities = map[string]bool{"urgent": true, "high": true, "medium": true, "low": true, "none": true}

type Queryer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type TxStarter interface {
	Begin(context.Context) (pgx.Tx, error)
}

type Store interface {
	ExportProject(context.Context, pgtype.UUID, pgtype.UUID) (Bundle, error)
	PreviewPortability(context.Context, pgtype.UUID, *pgtype.UUID, Bundle, ApplyOptions) (PortabilityPreview, error)
	DryRunPortability(context.Context, pgtype.UUID, *pgtype.UUID, Bundle, ApplyOptions) (PortabilityResult, error)
	ApplyPortability(context.Context, pgtype.UUID, pgtype.UUID, *pgtype.UUID, Bundle, ApplyOptions) (PortabilityResult, error)
}

type ApplyOptions struct {
	CreateMissingLabels bool
}

type PostgresStore struct {
	Queries      *db.Queries
	DB           Queryer
	TxStarter    TxStarter
	Entitlements entitlement.Provider
}

func NewPostgresStore(queries *db.Queries, executor Queryer, txStarter TxStarter, entitlements entitlement.Provider) *PostgresStore {
	return &PostgresStore{Queries: queries, DB: executor, TxStarter: txStarter, Entitlements: entitlements}
}

func ParseInputToBundle(projectTitleHint string, bundleJSON []byte, markdown string) (Bundle, error) {
	hasJSON := len(strings.TrimSpace(string(bundleJSON))) > 0
	hasMarkdown := strings.TrimSpace(markdown) != ""
	if hasJSON == hasMarkdown {
		return Bundle{}, errors.New("provide exactly one of bundle or markdown")
	}
	if hasJSON {
		return DecodeBundle(bundleJSON)
	}
	return ParseMarkdownToBundle(projectTitleHint, markdown)
}

func (s *PostgresStore) ExportProject(ctx context.Context, workspaceID, projectID pgtype.UUID) (Bundle, error) {
	if s == nil || s.TxStarter == nil || s.Queries == nil {
		return Bundle{}, errors.New("project portability database is unavailable")
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return Bundle{}, fmt.Errorf("begin project export snapshot: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
		return Bundle{}, fmt.Errorf("set project export snapshot: %w", err)
	}
	bundle, err := exportProject(ctx, tx, workspaceID, projectID)
	if err != nil {
		return Bundle{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Bundle{}, fmt.Errorf("finish project export snapshot: %w", err)
	}
	return bundle, nil
}

func (s *PostgresStore) PreviewPortability(ctx context.Context, workspaceID pgtype.UUID, projectID *pgtype.UUID, bundle Bundle, options ApplyOptions) (PortabilityPreview, error) {
	if s == nil || s.DB == nil || s.TxStarter == nil || s.Queries == nil {
		return PortabilityPreview{}, errors.New("project portability database is unavailable")
	}
	if err := ValidateBundle(bundle); err != nil {
		return PortabilityPreview{}, err
	}
	current := Bundle{SchemaVersion: SchemaVersionV1, Project: PortableProject{}, Tasks: []PortableTask{}}
	if projectID != nil && projectID.Valid {
		var err error
		current, err = s.ExportProject(ctx, workspaceID, *projectID)
		if err != nil {
			return PortabilityPreview{}, err
		}
	}
	preview := BuildPreview(current, bundle, projectID == nil || !projectID.Valid)
	if err := rejectIdentityConflicts(preview); err != nil {
		return PortabilityPreview{}, err
	}
	if err := s.addReferenceWarnings(ctx, s.DB, workspaceID, bundle, &preview, options); err != nil {
		return PortabilityPreview{}, err
	}
	return preview, nil
}

func (s *PostgresStore) ApplyPortability(ctx context.Context, workspaceID, userID pgtype.UUID, projectID *pgtype.UUID, bundle Bundle, options ApplyOptions) (PortabilityResult, error) {
	if err := ValidateBundle(bundle); err != nil {
		return PortabilityResult{}, err
	}
	if projectID == nil || !projectID.Valid {
		if strings.TrimSpace(bundle.Project.Title) == "" {
			return PortabilityResult{}, errors.New("project.title is required")
		}
	}
	if s == nil || s.TxStarter == nil || s.Queries == nil {
		return PortabilityResult{}, errors.New("project portability database is unavailable")
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return PortabilityResult{}, fmt.Errorf("begin project portability transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := s.Queries.WithTx(tx)
	result, err := s.applyInTransaction(ctx, tx, qtx, workspaceID, userID, projectID, bundle, options)
	if err != nil {
		return PortabilityResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PortabilityResult{}, fmt.Errorf("commit project portability transaction: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) DryRunPortability(ctx context.Context, workspaceID pgtype.UUID, projectID *pgtype.UUID, bundle Bundle, options ApplyOptions) (PortabilityResult, error) {
	preview, err := s.PreviewPortability(ctx, workspaceID, projectID, bundle, options)
	if err != nil {
		return PortabilityResult{}, err
	}
	result := resultFromPreview(preview)
	if projectID != nil && projectID.Valid {
		result.ProjectID = pgtypeUUIDString(*projectID)
	}
	result.Preview = &preview
	return result, nil
}

func resultFromPreview(preview PortabilityPreview) PortabilityResult {
	result := PortabilityResult{Warnings: append([]string{}, preview.Warnings...), Preview: nil}
	for _, task := range preview.Tasks {
		switch task.Action {
		case "create":
			result.CreatedCount++
		case "update":
			result.UpdatedCount++
		default:
			result.UnchangedCount++
		}
	}
	return result
}
