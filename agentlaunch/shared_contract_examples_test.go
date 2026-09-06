package agentlaunch_test

import (
	"testing"

	"github.com/hollis-labs/agentkit/agentcontext"
	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/artifact"
	"github.com/hollis-labs/agentkit/materialize"
)

func TestSharedMaterializationContractsCoverEntrypoints(t *testing.T) {
	tree := artifact.Tree{Entries: []artifact.Entry{
		{
			Path:      "AGENTS.md",
			Kind:      artifact.EntryFile,
			Mode:      0o644,
			Bytes:     []byte("synthetic boot body\n"),
			Ownership: artifact.Ownership{EntryID: "boot-agents", GroupID: "boot"},
		},
		{Path: "tasks", Kind: artifact.EntryDirectory, Mode: 0o755},
		{
			Path:      "tasks/T-1/task.md",
			Kind:      artifact.EntryFile,
			Mode:      0o644,
			Bytes:     []byte("synthetic task\n"),
			Ownership: artifact.Ownership{EntryID: "task-body", GroupID: "tasks"},
		},
	}}
	if err := tree.Validate(); err != nil {
		t.Fatalf("artifact tree contract invalid: %v", err)
	}

	direct := agentlaunch.PrepareRequest{
		Kind:      agentlaunch.PrepareInputArtifacts,
		Artifacts: &tree,
		Roots:     roots(),
		Access:    requiredLocalAccess(),
	}
	if err := direct.Validate(); err != nil {
		t.Fatalf("direct artifact prepare request invalid: %v", err)
	}

	resolved := agentcontext.ResolvedComposition{
		ID: "torque-task-bundle",
		Documents: []agentcontext.Document{{
			ID:   "agents",
			Path: "AGENTS.md",
			Sections: []agentcontext.Section{{
				ID:          "task",
				Content:     "synthetic task context",
				Contributor: "torque",
			}},
		}},
		Artifacts: tree,
	}
	fromComposition := agentlaunch.PrepareRequest{
		Kind:        agentlaunch.PrepareInputResolvedComposition,
		Composition: &resolved,
		Roots:       roots(),
		Access:      requiredLocalAccess(),
	}
	if err := fromComposition.Validate(); err != nil {
		t.Fatalf("resolved composition prepare request invalid: %v", err)
	}

	recipe := agentcontext.AuthoredRecipe{
		ID:         "cairn-install",
		Base:       "base-agent",
		Parts:      []agentcontext.PartRef{{ID: "install-docs"}},
		MergeRules: []agentcontext.MergeRule{{Field: "documents.sections", Kind: agentcontext.MergeAppend}},
	}
	fromRecipe := agentlaunch.PrepareRequest{
		Kind:   agentlaunch.PrepareInputAuthoredRecipe,
		Recipe: &recipe,
		Roots:  roots(),
		Access: requiredLocalAccess(),
	}
	if err := fromRecipe.Validate(); err != nil {
		t.Fatalf("authored recipe prepare request invalid: %v", err)
	}

	mreq := materialize.Request{
		Operation:      materialize.OperationCreate,
		TargetRoot:     "/tmp/agentkit-contract/boot",
		Roots:          materialize.TargetRoots{ProjectRoot: "/tmp/agentkit-contract/project", BootRoot: "/tmp/agentkit-contract/boot"},
		Artifacts:      tree,
		ExistingTarget: materialize.ExistingTargetRefuse,
	}
	if err := mreq.Validate(); err != nil {
		t.Fatalf("materialize request contract invalid: %v", err)
	}

	prepared := agentlaunch.PreparedExecution{
		InputKind: direct.Kind,
		Artifacts: tree,
		Bindings: agentlaunch.ExecutionBindings{
			Argv: []string{"codex", "exec", "--json"},
			CWD:  "/tmp/agentkit-contract/project",
			Env: map[string]agentlaunch.EnvVar{
				"CODEX_HOME": {Value: "/tmp/agentkit-contract/boot", Source: "provider-projection"},
			},
		},
		Roots:  roots(),
		Access: requiredLocalAccess(),
	}
	if err := prepared.Validate(); err != nil {
		t.Fatalf("prepared execution contract invalid: %v", err)
	}
}

func roots() agentlaunch.ExecutionRoots {
	return agentlaunch.ExecutionRoots{
		ProjectRoot: "/tmp/agentkit-contract/project",
		BootRoot:    "/tmp/agentkit-contract/boot",
		StateRoot:   "/tmp/agentkit-contract/state",
		ScratchRoot: "/tmp/agentkit-contract/scratch",
		CWD:         "/tmp/agentkit-contract/project",
	}
}

func requiredLocalAccess() agentlaunch.AccessRequirements {
	r := roots()
	return agentlaunch.AccessRequirements{
		Mode:  agentlaunch.AccessRequired,
		Host:  agentlaunch.ExecutionHostLocal,
		Roots: r,
		Filesystem: []agentlaunch.AccessPath{
			{Mode: agentlaunch.AccessRead, Root: agentlaunch.RootBoot, Path: r.BootRoot},
			{Mode: agentlaunch.AccessWrite, Root: agentlaunch.RootProject, Path: r.ProjectRoot},
			{Mode: agentlaunch.AccessWrite, Root: agentlaunch.RootState, Path: r.StateRoot},
			{Mode: agentlaunch.AccessWrite, Root: agentlaunch.RootScratch, Path: r.ScratchRoot},
			{Mode: agentlaunch.AccessDeny, Root: agentlaunch.RootOther, Path: "/tmp/agentkit-contract/denied"},
		},
		Network:    agentlaunch.NetworkAccess{Loopback: true},
		Subprocess: agentlaunch.SubprocessAccess{Allowed: true},
	}
}
