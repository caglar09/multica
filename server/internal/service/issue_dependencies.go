package service

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ErrIssueBlockedByDependency is returned before an agent task is persisted.
// Keeping this check in the shared enqueue path prevents assignment, mention,
// retry and autonomous dispatch from bypassing the same dependency rule.
var ErrIssueBlockedByDependency = errors.New("issue is blocked by an unresolved dependency")

func ensureIssueDependencyReady(ctx context.Context, q *db.Queries, workspaceID, issueID pgtype.UUID) error {
	blocked, err := q.IssueHasUnresolvedDependencies(ctx, db.IssueHasUnresolvedDependenciesParams{
		WorkspaceID: workspaceID,
		IssueID:     issueID,
	})
	if err != nil {
		return errors.Join(errors.New("check issue dependencies"), err)
	}
	if blocked {
		return ErrIssueBlockedByDependency
	}
	return nil
}
