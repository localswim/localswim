package v2

import (
	"net/url"
	"testing"
)

func TestNativeCardListRejectsForeignLaneFilters(t *testing.T) {
	state := nativeVerificationFixture()
	source := NativeBoard{state: state}
	valid := state.Lanes[0].ID
	foreign := newID()
	for _, ids := range [][]string{{foreign}, {valid, foreign}} {
		query := url.Values{"lane_id": ids}
		if _, err := source.ListCardsJSON(query.Encode()); err == nil {
			t.Fatalf("accepted foreign lanes %v", ids)
		}
	}
	if _, err := source.ListCardsJSON(url.Values{"lane_id": {valid}}.Encode()); err != nil {
		t.Fatal(err)
	}
}
