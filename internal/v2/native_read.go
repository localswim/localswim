package v2

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
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
	for id := range filters.laneIDs {
		if !slices.ContainsFunc(b.state.Lanes, func(item lane) bool { return item.ID == id }) {
			return nil, invalid("lane_id", "Lane does not belong to this board.")
		}
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

// InspectCardJSON resolves a card within this validated saved board.
func (b NativeBoard) InspectCardJSON(cardID string, includeProse bool) (json.RawMessage, error) {
	for _, card := range b.state.Cards {
		if card.ID == cardID {
			return b.InspectTicketJSON(card.Ticket, includeProse)
		}
	}
	return nil, errors.New("card does not belong to the saved board")
}

// CardTicket resolves the board-local ticket for native card-filtered reads.
func (b NativeBoard) CardTicket(cardID string) (uint64, error) {
	for _, card := range b.state.Cards {
		if card.ID == cardID {
			return card.Ticket, nil
		}
	}
	return 0, errors.New("card does not belong to the saved board")
}
