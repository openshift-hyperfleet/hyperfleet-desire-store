package services

import (
	"fmt"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
)

// operation is a REST v1 operation that the Caller-to-Operation matrix
// restricts. List desires and Get desire are open to every caller in the
// partition, so they have no entry.
type operation string

const (
	opWrite       operation = "write desire"
	opWriteStatus operation = "write status"
	opDelete      operation = "delete desire"
)

// role is the caller as the Caller-to-Operation matrix names it.
type role string

const (
	roleApplier role = "applier"
	roleAdapter role = "adapter"
	roleSweeper role = "sweeper"
)

// roleOf tells the callers apart from Wristband claims only: the caller class
// separates the remote Applier from fleet-scoped callers, and the sweeper is
// the fleet-scoped caller whose hf_identity is the configured sweeper identity.
func roleOf(caller auth.Caller, sweeperIdentity string) role {
	if caller.Class == auth.ClassRemoteApplier {
		return roleApplier
	}
	if caller.Identity == sweeperIdentity {
		return roleSweeper
	}
	return roleAdapter
}

// authorizeOperation applies the Caller-to-Operation matrix. The desire type
// matters only to the sweeper, which may write Delete desires only.
func authorizeOperation(caller auth.Caller, sweeperIdentity string, op operation,
	desireType desire.DesireType,
) *errors.ServiceError {
	who := roleOf(caller, sweeperIdentity)
	allowed := false
	switch op {
	case opWrite:
		allowed = who == roleAdapter || (who == roleSweeper && desireType == desire.TypeDelete)
	case opWriteStatus:
		allowed = who == roleApplier
	case opDelete:
		allowed = who == roleAdapter || who == roleSweeper
	}
	if !allowed {
		return errors.Forbidden(errors.CodePermissionDenied, fmt.Sprintf("The caller is not permitted to %s.", op))
	}
	return nil
}

// authorizePartition rejects a path partition that is not the partition in
// the caller's Wristband. The detailed design has the gateway set that claim
// to the requested partition for a fleet-scoped caller and to the bound
// partition for a remote Applier (Partition Selection, Fleet-Scoped Caller
// AuthConfig).
func authorizePartition(caller auth.Caller, pathPartition string) *errors.ServiceError {
	if pathPartition != caller.Partition {
		return errors.Forbidden(errors.CodePartitionDenied, "The path partition is outside the caller's authorization.")
	}
	return nil
}
