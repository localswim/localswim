package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfiguredMoveCanonicalOmissionAndExclusivity(t *testing.T) {
	freeze := func(input string) (Intent, error) {
		return Freeze(sample("card.edit", cardC, input), ResolvedRoute{"card.edit", board, cardC}, AuthenticatedActor{actor})
	}
	legacy, err := freeze(`{"lane":"In Progress"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy.CanonicalBytes()), "laneId") {
		t.Fatal("new field padded old original")
	}
	newIntent, err := freeze(`{"laneId":"60000000-0000-0000-0000-000000000099"}`)
	if err != nil {
		t.Fatal(err)
	}
	input, err := newIntent.Input()
	if err != nil {
		t.Fatal(err)
	}
	move := input.(*EditInput)
	if move.LaneID == nil || *move.LaneID != "60000000-0000-0000-0000-000000000099" || move.Lane != nil {
		t.Fatal("move input lost exact UUID")
	}
	b, _ := json.Marshal(move)
	if !strings.Contains(string(b), `"lane":null`) || !strings.Contains(string(b), `"laneId":`) {
		t.Fatal("normalized payload changed")
	}
	for _, input := range []string{`{"laneId":null}`, `{"laneId":"foreign"}`, `{"laneId":"60000000-0000-0000-0000-000000000001","title":"x"}`, `{"laneId":"60000000-0000-0000-0000-000000000001","lane":"In Progress"}`, `{"laneId":"60000000-0000-0000-0000-000000000001","lane":null}`} {
		if _, err := freeze(input); err == nil {
			t.Fatal("invalid move accepted", input)
		}
	}
}
