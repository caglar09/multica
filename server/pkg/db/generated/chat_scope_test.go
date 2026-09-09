package db

import (
	"strings"
	"testing"
)

// Project-owned history stays accessible by session ID to the embedded panel,
// but must not leak into either global history view or its running indicator.
func TestGlobalChatQueriesExcludeProjectSessions(t *testing.T) {
	for name, query := range map[string]string{
		"active":   listChatSessionsByCreator,
		"archived": listAllChatSessionsByCreator,
		"running":  listPendingChatTasksByCreator,
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(query, "AND cs.session_kind = 'standard'") {
				t.Fatal("global chat query must include only standard sessions")
			}
		})
	}
	if strings.Contains(getChatSession, "session_kind = 'standard'") {
		t.Fatal("project panel must still be able to load its session")
	}
}
