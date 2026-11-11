package v2

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

const activationDirectory = ".localswim-activations"

var errActivationStore = errors.New("activation outcome unavailable")

type activation struct {
	ID        string  `json:"id"`
	BuildID   string  `json:"build_id"`
	State     string  `json:"state"`
	ErrorCode *string `json:"error_code"`
}

type activationRecord struct {
	activation
	Origin  string `json:"origin"`
	Version string `json:"staged_version"`
	Digest  string `json:"staged_digest"`
}

func (s *Service) activationRecord(id string) (activationRecord, error) {
	root, err := os.OpenRoot(s.store.root)
	if err != nil {
		return activationRecord{}, errActivationStore
	}
	defer root.Close()
	dir, err := root.Lstat(activationDirectory)
	if errors.Is(err, fs.ErrNotExist) {
		return activationRecord{}, errMissing
	}
	if err != nil || !dir.IsDir() {
		return activationRecord{}, errActivationStore
	}
	raw, err := readBuildFile(root, filepath.Join(activationDirectory, id+".json"), buildManifestLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return activationRecord{}, errMissing
	}
	if err != nil {
		return activationRecord{}, errActivationStore
	}
	fields, err := decodeObject(raw)
	var result activationRecord
	if err != nil || len(fields) != 7 || fields["error_code"] == nil ||
		unmarshalCanonical(raw, &result) != nil || result.ID != id || !validSnapshotOperationID(result.ID) || !validSnapshotID(result.BuildID) ||
		!validBuildManifestFields(result.Version, result.Digest) {
		return activationRecord{}, errActivationStore
	}
	if !validSnapshotOperationID(result.Origin) {
		return activationRecord{}, errActivationStore
	}
	switch result.State {
	case "accepted", "activated":
		if result.ErrorCode == nil {
			return result, nil
		}
	case "failed":
		if result.ErrorCode != nil && *result.ErrorCode == "activation_handoff_failed" {
			return result, nil
		}
	}
	return activationRecord{}, errActivationStore
}

func (s *Service) saveActivation(record activationRecord) error {
	dir := filepath.Join(s.store.root, activationDirectory)
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return errActivationStore
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return errActivationStore
	}
	data, err := marshalCanonical(record)
	if err != nil {
		return errActivationStore
	}
	if err := writeAtomic(filepath.Join(dir, record.ID+".json"), data); err != nil {
		if errors.Is(err, errWriteUncertain) {
			return errWriteUncertain
		}
		return errActivationStore
	}
	return nil
}

func (x *request) readActivation(id string) {
	record, err := x.service.activationRecord(id)
	if err != nil {
		x.fail(err)
		return
	}
	currentInstance := x.service.builds.service().ServiceInstanceID
	if record.State == "accepted" && (record.Origin != currentInstance || !x.service.lifecycle.activating(id)) ||
		record.State == "activated" && record.Origin == currentInstance {
		code := "activation_interrupted"
		record.State, record.ErrorCode = "failed", &code
	}
	x.respond(http.StatusOK, record.activation, "")
}

func (x *request) activateBuild() {
	fields, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	if err := rejectFields(fields, []string{"id", "build_id", "expected_service_etag"}, true); err != nil {
		x.fail(err)
		return
	}
	id := newOperationID()
	if raw, ok := fields["id"]; ok {
		id, err = inputOperationID(raw, "id")
		if err != nil {
			x.fail(err)
			return
		}
	}
	buildID, err := inputID(fields["build_id"], "build_id")
	if err != nil {
		x.fail(err)
		return
	}
	expected, err := approvalTag(fields["expected_service_etag"], "expected_service_etag")
	if err != nil {
		var missing approvalPreconditionRequired
		if errors.As(err, &missing) {
			x.problem(http.StatusPreconditionRequired, "precondition_required", missing.Error(), "", nil)
		} else {
			x.fail(err)
		}
		return
	}
	s := x.service
	running := s.builds.service()
	if _, err = s.lifecycle.begin(running, expected, id); err != nil {
		x.fail(err)
		return
	}
	exit, err := s.prepareBuildActivation(id, buildID, running.ServiceInstanceID)
	if err != nil {
		x.fail(err)
		return
	}
	x.w.Header().Set("Location", Prefix+"/activations/"+id)
	x.respond(http.StatusAccepted, activation{ID: id, BuildID: buildID, State: "accepted"}, "")
	s.lifecycle.exit <- exit
}

func (s *Service) prepareBuildActivation(id, buildID, instance string) (exit ServiceExit, result error) {
	var failure string
	defer func() {
		if result != nil {
			s.lifecycle.resume(failure)
		}
	}()
	if _, err := s.activationRecord(id); !errors.Is(err, errMissing) {
		if err == nil {
			err = errActivationID
		}
		return ServiceExit{}, err
	}
	image, err := s.builds.activationImage(buildID)
	if err != nil {
		return ServiceExit{}, err
	}
	if s.lifecycle.prepare == nil {
		failure = "activation_prepare_failed"
		return ServiceExit{}, errActivationStart
	}
	replacement, err := s.lifecycle.prepare(image.bytes, id)
	if err != nil {
		failure = "activation_prepare_failed"
		return ServiceExit{}, errActivationStart
	}
	defer func() {
		if result != nil {
			_ = replacement.Abort()
		}
	}()
	if err := s.drain(); err != nil {
		failure = publicationFailureCode(err)
		return ServiceExit{}, errFinalPublish
	}
	record := activationRecord{
		activation: activation{ID: id, BuildID: buildID, State: "accepted"},
		Origin:     instance,
		Version:    image.version,
		Digest:     image.digest,
	}
	if err := s.saveActivation(record); err != nil {
		failure = "activation_record_failed"
		return ServiceExit{}, err
	}
	return ServiceExit{ActivationID: id, Replacement: replacement}, nil
}

// CompleteActivation runs in the replacement with the writer lease held and
// before its listener starts admitting requests.
func (s *Service) CompleteActivation(id string) error {
	if !validSnapshotOperationID(id) {
		return errActivationStore
	}
	record, err := s.activationRecord(id)
	if err != nil || record.State != "accepted" {
		return errActivationStore
	}
	record.State = "activated"
	if err := s.saveActivation(record); err != nil {
		return err
	}
	s.builds.running.BuildID = record.BuildID
	s.builds.activeVersion, s.builds.activeDigest = record.Version, record.Digest
	return nil
}

func (s *Service) failActivation(id string) error {
	const code = "activation_handoff_failed"
	record, err := s.activationRecord(id)
	if err == nil {
		record.State = "failed"
		failure := code
		record.ErrorCode = &failure
		err = s.saveActivation(record)
	}
	if err != nil {
		s.lifecycle.resume("activation_record_failed")
		return err
	}
	s.lifecycle.resume(code)
	return nil
}
