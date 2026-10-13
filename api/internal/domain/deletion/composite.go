package deletion

import (
	"context"

	"gopkg.aoctech.app/api-commons/erasure"
)

// RemoteEligibility asks the participating products for blockers.
type RemoteEligibility interface {
	Check(ctx context.Context, userID string, workspaceIDs []string) ([]erasure.Blocker, error)
}

// CompositeOwnership adds the participants' blockers to the local assessment.
type CompositeOwnership struct {
	local  Ownership
	remote RemoteEligibility
}

func NewCompositeOwnership(local Ownership, remote RemoteEligibility) *CompositeOwnership {
	return &CompositeOwnership{local: local, remote: remote}
}

func (c *CompositeOwnership) Assess(ctx context.Context, userID string) ([]Workspace, []erasure.Blocker, error) {
	sole, blockers, err := c.local.Assess(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(sole))
	for _, w := range sole {
		ids = append(ids, w.ID)
	}
	remote, err := c.remote.Check(ctx, userID, ids)
	if err != nil {
		return nil, nil, err
	}
	return sole, append(blockers, remote...), nil
}
