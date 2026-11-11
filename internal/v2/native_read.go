package v2

import (
	"encoding/json"
	"errors"
	"path/filepath"
)

// NativeBoard holds one validated committed snapshot read directly from disk.
type NativeBoard struct {
	state snapshot
}

// OpenNativeBoard reads the saved board without acquiring a writer or starting
// a service. Direct filesystem access to the supplied data directory is required.
func OpenNativeBoard(root, boardID string) (NativeBoard, error) {
	if root == "" {
		return NativeBoard{}, errors.New("native reads require a data directory")
	}
	id, ok := normalizeID(boardID)
	if !ok {
		return NativeBoard{}, errors.New("invalid board ID")
	}
	state, err := readSnapshot(filepath.Join(root, "boards", id+".json"), id)
	if err != nil {
		return NativeBoard{}, err
	}
	return NativeBoard{state}, nil
}

func (b NativeBoard) MetadataJSON() (json.RawMessage, error) {
	return marshalCanonical(b.state.Board)
}

func (b NativeBoard) ExportJSON() (json.RawMessage, error) {
	return marshalCanonical(b.state)
}

func (b NativeBoard) InspectTicketJSON(ticket uint64, includeProse bool) (json.RawMessage, error) {
	result, err := InspectTicket(b.state, ticket, includeProse)
	if err != nil {
		return nil, err
	}
	return marshalCanonical(result)
}
