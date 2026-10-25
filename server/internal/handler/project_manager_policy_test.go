package handler

import "testing"

func TestProjectManagerChatNeedsReadOnly(t *testing.T) {
	tests := []struct {
		name           string
		sessionKind    string
		projectManager bool
		want           bool
	}{
		{name: "standard project manager chat", sessionKind: "standard", projectManager: true, want: true},
		{name: "standard non manager chat", sessionKind: "standard", projectManager: false, want: false},
		{name: "project leader session", sessionKind: "autonomous_project_leader", want: true},
		{name: "project planning session", sessionKind: "autonomous_project_planning", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := projectManagerChatNeedsReadOnly(tt.sessionKind, tt.projectManager); got != tt.want {
				t.Fatalf("projectManagerChatNeedsReadOnly(%q, %t) = %t, want %t", tt.sessionKind, tt.projectManager, got, tt.want)
			}
		})
	}
}
