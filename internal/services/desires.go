// Package services holds the Desire Store business rules: who may do what,
// which desires the store accepts, and how store failures map to errors. It
// knows nothing about HTTP; the handlers decode requests into its inputs and
// present its results.
package services

import (
	"context"
	stderrors "errors"
	"fmt"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
)

// Store is the shared desire store interface the service runs on.
type Store interface {
	desire.SpecStore
	desire.StatusStore
}

// DesireService serves the five REST v1 operations. Every method authorizes
// the caller before it touches the store.
type DesireService interface {
	List(ctx context.Context, caller auth.Caller, partition string, query ListQuery) (Page, *errors.ServiceError)
	Get(ctx context.Context, caller auth.Caller, id desire.Identity) (Desire, *errors.ServiceError)
	// Write creates a desire, or updates an ApplyDesire spec when the input
	// carries a version. created reports which one happened.
	Write(ctx context.Context, caller auth.Caller, id desire.Identity, input WriteInput) (
		stored Desire, created bool, err *errors.ServiceError)
	WriteStatus(ctx context.Context, caller auth.Caller, id desire.Identity, input StatusInput) *errors.ServiceError
	Delete(ctx context.Context, caller auth.Caller, id desire.Identity, version int64) *errors.ServiceError
}

// Desire is one stored desire. Exactly one field is set.
type Desire struct {
	Apply  *desire.ApplyDesire
	Delete *desire.DeleteDesire
	Read   *desire.ReadDesire
}

// Version returns the stored compare-and-swap version.
func (d Desire) Version() int64 {
	switch {
	case d.Apply != nil:
		return d.Apply.Version
	case d.Delete != nil:
		return d.Delete.Version
	case d.Read != nil:
		return d.Read.Version
	default:
		return 0
	}
}

// WriteInput is a decoded Write desire request. Version is 0 for a create.
type WriteInput struct {
	ResourceID    string
	Version       int64
	KubeContent   []byte
	TargetVersion string
}

// StatusInput is a decoded Write status request. Status is the status of an
// ApplyDesire or DeleteDesire, ReadStatus the status of a ReadDesire.
type StatusInput struct {
	Version    int64
	Status     desire.Status
	ReadStatus desire.ReadStatus
}

// fieldBody names the request body in a validation error.
const fieldBody = "body"

type desireService struct {
	store           Store
	sweeperIdentity string
}

// NewDesireService serves the store; sweeperIdentity is the hf_identity that
// the sweeper's Wristbands carry.
func NewDesireService(store Store, sweeperIdentity string) DesireService {
	return &desireService{store: store, sweeperIdentity: sweeperIdentity}
}

// List lists the partition's desires. Every caller in the partition may list.
func (s *desireService) List(ctx context.Context, caller auth.Caller, partition string, query ListQuery) (
	Page, *errors.ServiceError,
) {
	if err := authorizePartition(caller, partition); err != nil {
		return Page{}, err
	}
	var refs []Reference
	for _, desireType := range query.Types {
		typed, err := s.references(ctx, partition, desireType)
		if err != nil {
			return Page{}, storeError(err, desire.Identity{}, 0, nil)
		}
		refs = append(refs, typed...)
	}
	return pageOf(refs, query), nil
}

// Get reads one desire. Every caller in the partition may get.
func (s *desireService) Get(ctx context.Context, caller auth.Caller, id desire.Identity) (
	Desire, *errors.ServiceError,
) {
	if err := authorizePartition(caller, id.ManagementCluster); err != nil {
		return Desire{}, err
	}
	d, err := s.fetch(ctx, id)
	if err != nil {
		return Desire{}, storeError(err, id, 0, nil)
	}
	return d, nil
}

// Write creates a desire, or updates an ApplyDesire spec when the input
// carries a version. The owner is always the caller's hf_identity. The desire
// is checked against the shared store's validation first, so the store never
// receives a desire it would reject.
func (s *desireService) Write(ctx context.Context, caller auth.Caller, id desire.Identity, input WriteInput) (
	Desire, bool, *errors.ServiceError,
) {
	if err := s.authorize(caller, id, opWrite); err != nil {
		return Desire{}, false, err
	}
	// The handlers accept a version only on an ApplyDesire.
	if id.Type == desire.TypeApply && input.Version > 0 {
		stored, err := s.updateSpec(ctx, caller.Identity, id, input)
		return stored, false, err
	}
	stored, err := s.create(ctx, caller.Identity, id, input)
	return stored, err == nil, err
}

func (s *desireService) updateSpec(ctx context.Context, owner string, id desire.Identity, input WriteInput) (
	Desire, *errors.ServiceError,
) {
	spec := desire.ApplySpec{KubeContent: input.KubeContent}
	if invalid := spec.Validate(); invalid != nil {
		return Desire{}, invalidDesire(invalid)
	}
	d, err := s.store.UpdateApplyDesireSpec(ctx, id, spec, owner, input.Version)
	if err != nil {
		return Desire{}, s.failed(ctx, err, id, input.Version)
	}
	return Desire{Apply: &d}, nil
}

func (s *desireService) create(ctx context.Context, owner string, id desire.Identity, input WriteInput) (
	Desire, *errors.ServiceError,
) {
	var (
		stored       Desire
		invalid, err error
	)
	switch id.Type {
	case desire.TypeApply:
		d := desire.ApplyDesire{
			Identity: id, Owner: owner, OriginID: input.ResourceID,
			Spec: desire.ApplySpec{KubeContent: input.KubeContent},
		}
		if invalid = d.Validate(); invalid == nil {
			d, err = s.store.CreateApplyDesire(ctx, d)
		}
		stored = Desire{Apply: &d}
	case desire.TypeDelete:
		d := desire.DeleteDesire{Identity: id, Owner: owner, OriginID: input.ResourceID}
		if invalid = d.Validate(); invalid == nil {
			d, err = s.store.CreateDeleteDesire(ctx, d)
		}
		stored = Desire{Delete: &d}
	case desire.TypeRead:
		d := desire.ReadDesire{
			Identity: id, Owner: owner, OriginID: input.ResourceID, TargetVersion: input.TargetVersion,
		}
		if invalid = d.Validate(); invalid == nil {
			d, err = s.store.CreateReadDesire(ctx, d)
		}
		stored = Desire{Read: &d}
	default:
		err = unknownType(id.Type)
	}
	switch {
	case invalid != nil:
		return Desire{}, invalidDesire(invalid)
	case err != nil:
		return Desire{}, s.failed(ctx, err, id, 0)
	default:
		return stored, nil
	}
}

// WriteStatus writes the status the Applier reports. It does not check the
// owner.
func (s *desireService) WriteStatus(ctx context.Context, caller auth.Caller, id desire.Identity,
	input StatusInput,
) *errors.ServiceError {
	if err := s.authorize(caller, id, opWriteStatus); err != nil {
		return err
	}
	var err error
	switch id.Type {
	case desire.TypeApply:
		_, err = s.store.UpdateApplyDesireStatus(ctx, id, input.Status, input.Version)
	case desire.TypeDelete:
		_, err = s.store.UpdateDeleteDesireStatus(ctx, id, input.Status, input.Version)
	case desire.TypeRead:
		_, err = s.store.UpdateReadDesireStatus(ctx, id, input.ReadStatus)
	default:
		err = unknownType(id.Type)
	}
	if err != nil {
		return s.failed(ctx, err, id, input.Version)
	}
	return nil
}

// Delete removes the desire, checked against the version and the owner.
func (s *desireService) Delete(ctx context.Context, caller auth.Caller, id desire.Identity, version int64,
) *errors.ServiceError {
	if err := s.authorize(caller, id, opDelete); err != nil {
		return err
	}
	var err error
	switch id.Type {
	case desire.TypeApply:
		err = s.store.DeleteApplyDesire(ctx, id, caller.Identity, version)
	case desire.TypeDelete:
		err = s.store.DeleteDeleteDesire(ctx, id, caller.Identity, version)
	case desire.TypeRead:
		err = s.store.DeleteReadDesire(ctx, id, caller.Identity, version)
	default:
		err = unknownType(id.Type)
	}
	if err != nil {
		return s.failed(ctx, err, id, version)
	}
	return nil
}

// authorize checks the partition, then the Caller-to-Operation matrix.
func (s *desireService) authorize(caller auth.Caller, id desire.Identity, op operation) *errors.ServiceError {
	if err := authorizePartition(caller, id.ManagementCluster); err != nil {
		return err
	}
	return authorizeOperation(caller, s.sweeperIdentity, op, id.Type)
}

func (s *desireService) references(ctx context.Context, partition string, desireType desire.DesireType) (
	[]Reference, error,
) {
	var refs []Reference
	switch desireType {
	case desire.TypeApply:
		desires, err := s.store.ListApplyDesires(ctx, partition)
		for _, d := range desires {
			refs = append(refs, Reference{Identity: d.Identity, Version: d.Version})
		}
		return refs, err
	case desire.TypeDelete:
		desires, err := s.store.ListDeleteDesires(ctx, partition)
		for _, d := range desires {
			refs = append(refs, Reference{Identity: d.Identity, Version: d.Version})
		}
		return refs, err
	case desire.TypeRead:
		desires, err := s.store.ListReadDesires(ctx, partition)
		for _, d := range desires {
			refs = append(refs, Reference{Identity: d.Identity, Version: d.Version, TargetVersion: d.TargetVersion})
		}
		return refs, err
	default:
		return nil, unknownType(desireType)
	}
}

func (s *desireService) fetch(ctx context.Context, id desire.Identity) (Desire, error) {
	switch id.Type {
	case desire.TypeApply:
		d, err := s.store.GetApplyDesire(ctx, id)
		return Desire{Apply: &d}, err
	case desire.TypeDelete:
		d, err := s.store.GetDeleteDesire(ctx, id)
		return Desire{Delete: &d}, err
	case desire.TypeRead:
		d, err := s.store.GetReadDesire(ctx, id)
		return Desire{Read: &d}, err
	default:
		return Desire{}, unknownType(id.Type)
	}
}

// failed maps a failed store write. After a version conflict it reads the
// stored version for the error; the read is not atomic with the failed write,
// so it reports the version at read time.
func (s *desireService) failed(ctx context.Context, err error, id desire.Identity, expected int64,
) *errors.ServiceError {
	var actual *int64
	if stderrors.Is(err, desire.ErrVersionConflict) {
		if stored, getErr := s.fetch(ctx, id); getErr == nil {
			actual = new(stored.Version())
		}
	}
	return storeError(err, id, expected, actual)
}

// storeError maps a store error to a ServiceError. An error that is not one
// of the store's sentinels means the store is degraded.
func storeError(err error, id desire.Identity, expected int64, actual *int64) *errors.ServiceError {
	switch {
	case stderrors.Is(err, desire.ErrNotFound):
		return errors.NotFound(fmt.Sprintf("Desire '%s' not found.", FormatDesireID(id)))
	case stderrors.Is(err, desire.ErrVersionConflict):
		return errors.VersionConflict(expected, actual)
	case stderrors.Is(err, desire.ErrAlreadyExists):
		return errors.Conflict(errors.CodeAlreadyExists, alreadyExistsReason(id.Type))
	case stderrors.Is(err, desire.ErrDeletePending):
		return errors.Conflict(errors.CodeDeletePending,
			"A DeleteDesire for this object is not confirmed yet. Retry after the Applier confirms the delete.")
	case stderrors.Is(err, desire.ErrOwnerConflict):
		return errors.Conflict(errors.CodeOwnerConflict, "A different caller owns this object.")
	case stderrors.Is(err, desire.ErrAborted):
		return errors.Unavailable(errors.CodeUnavailable,
			"A concurrent change aborted the request. Retry with backoff.", err)
	default:
		return errors.Unavailable(errors.CodeDependencyFailure, "The desire store is unavailable.", err)
	}
}

// alreadyExistsReason gives the HYPERFLEET-CNF-001 client action for the
// desire type. Only an ApplyDesire has an update; the other two types return
// 400 for a non-zero version.
func alreadyExistsReason(desireType desire.DesireType) string {
	switch desireType {
	case desire.TypeDelete:
		return "The desire already exists. The existing DeleteDesire already requests the delete."
	case desire.TypeRead:
		return "The desire already exists. Read it. " +
			"If its target_version differs, delete it with its version and create it again."
	default:
		return "The desire already exists. Read it, then update it with its version."
	}
}

// invalidDesire turns a failed shared-store validation into a 400.
func invalidDesire(err error) *errors.ServiceError {
	return errors.Invalid(errors.CodeInvalidField, fieldBody, "", "the desire is invalid: "+err.Error())
}

// unknownType reports a desire type the service does not serve. The handlers
// accept only the three known types, so this does not happen in practice.
func unknownType(desireType desire.DesireType) error {
	return fmt.Errorf("unknown desire type %q", desireType)
}

// FormatDesireID encodes an identity as a desire identifier: its fields joined
// by ':' in the order type, group, resource, namespace, name.
func FormatDesireID(id desire.Identity) string {
	return string(id.Type) + ":" + id.Group + ":" + id.Resource + ":" + id.Namespace + ":" + id.Name
}
