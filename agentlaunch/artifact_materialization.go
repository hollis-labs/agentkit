package agentlaunch

import (
	"context"
	"os"
	"time"

	"github.com/hollis-labs/agentkit/artifact"
	"github.com/hollis-labs/agentkit/materialize"
)

// ArtifactMaterializationRequest is the shared bridge from launch-facing
// compatibility adapters to the neutral materialization engine.
type ArtifactMaterializationRequest struct {
	TargetRoot string
	Roots      ExecutionRoots
	Artifacts  artifact.Tree

	Operation          materialize.Operation
	Generation         string
	ExpectedGeneration string
	Selection          materialize.Selection
	Reconcile          materialize.ReconcilePolicy
	Engine             materialize.Engine
	Now                func() time.Time
}

// MaterializeArtifacts applies an artifact tree through materialize.Engine.
// Existing directories use reconcile semantics by default so callers with a
// precreated bootdir can install or refresh without appending launch state or
// reimplementing the writer loop.
func MaterializeArtifacts(ctx context.Context, req ArtifactMaterializationRequest) (*materialize.Handle, error) {
	operation := req.Operation
	if operation == "" {
		operation = materialize.OperationReconcile
	}
	engine := req.Engine
	if engine == nil {
		engine = materialize.NewEngine(materialize.EngineOptions{Now: req.Now})
	}
	mreq := materialize.Request{
		Operation:          operation,
		TargetRoot:         req.TargetRoot,
		Roots:              materializeRoots(req.Roots),
		Artifacts:          req.Artifacts,
		Generation:         req.Generation,
		ExpectedGeneration: req.ExpectedGeneration,
		Selection:          req.Selection,
		Reconcile:          req.Reconcile,
	}
	if mreq.Reconcile.Conflict == "" {
		mreq.Reconcile.Conflict = materialize.ConflictOverwrite
	}
	if operation == materialize.OperationReconcile || operation == materialize.OperationRefresh {
		if err := os.MkdirAll(req.TargetRoot, 0o755); err != nil {
			return nil, err
		}
		manifest, err := materialize.LoadManifest(req.TargetRoot)
		if err == nil {
			mreq.CurrentManifest = &manifest
		} else if os.IsNotExist(err) {
			bootstrap := bootstrapManifest(req.TargetRoot, req.Roots, req.Artifacts, req.Generation, req.Now)
			mreq.CurrentManifest = &bootstrap
		} else {
			return nil, err
		}
	}
	handle, err := engine.Apply(ctx, mreq)
	if err != nil {
		return &handle, err
	}
	return &handle, nil
}

func materializeRoots(roots ExecutionRoots) materialize.TargetRoots {
	return materialize.TargetRoots{
		ProjectRoot: roots.ProjectRoot,
		BootRoot:    roots.BootRoot,
		StateRoot:   roots.StateRoot,
		ScratchRoot: roots.ScratchRoot,
	}
}

func bootstrapManifest(_ string, roots ExecutionRoots, tree artifact.Tree, generation string, now func() time.Time) materialize.Manifest {
	created := time.Now().UTC()
	if now != nil {
		created = now().UTC()
	}
	entries := make([]materialize.ManifestEntry, 0, len(tree.Entries))
	for _, entry := range tree.Entries {
		entries = append(entries, materialize.ManifestEntry{
			Path:       entry.Path,
			Kind:       entry.Kind,
			Mode:       uint32(artifactMode(entry)),
			Digest:     artifactDigest(entry),
			Ownership:  entry.Ownership,
			Provenance: entry.Provenance,
		})
	}
	return materialize.Manifest{
		SchemaVersion: "agentkit.materialize.v1",
		Generation:    generation,
		Roots:         materializeRoots(roots),
		Entries:       entries,
		CreatedAt:     created,
	}
}

func artifactMode(entry artifact.Entry) os.FileMode {
	if entry.Mode != 0 {
		return entry.Mode.Perm()
	}
	if entry.Kind == artifact.EntryDirectory {
		return 0o755
	}
	return 0o644
}

func artifactDigest(entry artifact.Entry) artifact.Digest {
	if entry.Digest.Algorithm != "" || entry.Digest.Hex != "" {
		return entry.Digest
	}
	if entry.Kind == artifact.EntryFile && entry.Bytes != nil {
		return artifact.DigestBytes(entry.Bytes)
	}
	return artifact.Digest{}
}
