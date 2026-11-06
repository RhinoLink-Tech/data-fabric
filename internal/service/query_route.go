package service

import (
	"context"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/replicarouting"
)

func querySessionSlug(ctx context.Context, id string, expiry time.Time, standalone func() string) (string, error) {
	if owner := replicarouting.OwnerFromContext(ctx); owner != nil {
		return owner.Mark(id, expiry)
	}
	return standalone(), nil
}
