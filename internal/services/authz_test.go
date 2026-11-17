package services

import (
	"fmt"
	"slices"
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth/authtest"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
)

const (
	testPartition = "mc-01"
	adapterA      = "system:serviceaccount:hyperfleet:cluster-adapter-hyperfleet-adapter"
	applierID     = "system:serviceaccount:hyperfleet:applier"
)

// TestCallerOperationMatrix checks every caller, operation, and desire type
// against the Caller-to-Operation matrix. The sweeper row is SWP-1.
func TestCallerOperationMatrix(t *testing.T) {
	callers := map[role]auth.Caller{
		roleApplier: {Class: auth.ClassRemoteApplier, Identity: applierID, Partition: testPartition},
		roleAdapter: {Class: auth.ClassFleetScoped, Identity: adapterA, Partition: testPartition},
		roleSweeper: {Class: auth.ClassFleetScoped, Identity: authtest.SweeperID, Partition: testPartition},
	}
	all := desire.AllTypes()
	none := []desire.DesireType{}
	// The Caller-to-Operation matrix of the detailed design, cell by cell.
	// List and Get are open to every caller; TestEveryCallerMayListAndGet
	// covers them.
	matrix := map[role]map[operation][]desire.DesireType{
		roleApplier: {opWrite: none, opWriteStatus: all, opDelete: none},
		roleAdapter: {opWrite: all, opWriteStatus: none, opDelete: all},
		roleSweeper: {opWrite: {desire.TypeDelete}, opWriteStatus: none, opDelete: all},
	}

	for who, caller := range callers {
		for _, op := range []operation{opWrite, opWriteStatus, opDelete} {
			for _, desireType := range desire.AllTypes() {
				name := fmt.Sprintf("%s/%s/%s", who, op, desireType)
				problem := authorizeOperation(caller, authtest.SweeperID, op, desireType)
				expected := slices.Contains(matrix[who][op], desireType)
				switch {
				case expected && problem != nil:
					t.Errorf("%s: denied with %s, want allowed", name, problem.Code)
				case !expected && (problem == nil || problem.Code != errors.CodePermissionDenied):
					t.Errorf("%s: problem = %+v, want 403 %s", name, problem, errors.CodePermissionDenied)
				}
			}
		}
	}
}
