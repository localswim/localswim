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

func (b NativeBoard) ListCardsJSON(rawQuery string) (json.RawMessage, error) {
	filters, err := readCardFilters(rawQuery)
	if err != nil {
		return nil, err
	}
	result, err := filters.page(b.state, rawQuery)
	if err != nil {
		return nil, err
	}
	return marshalNativePage(result)
}

func (b NativeBoard) ListCommentsJSON(rawQuery string) (json.RawMessage, error) {
	result, err := commentPage(b.state, rawQuery)
	if err != nil {
		return nil, err
	}
	return marshalNativePage(result)
}

func marshalNativePage(result map[string]any) (json.RawMessage, error) {
	data, err := marshalCanonical(result)
	if err != nil {
		return nil, err
	}
	if len(data) > maxJSONBytes {
		return nil, errors.New("saved board response exceeds 96 MiB")
	}
	return data, nil
}
