package v2

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestCardTitleWhitespaceValidation(t *testing.T) {
	app, client := eventHTTPServer(t)
	raw, _ := client.call("POST", "/boards", `{"name":"Title validation"}`, "", 201)
	board := turnOneDecode[board](t, raw)
	encode := func(value any) string {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	load := func() snapshot {
		t.Helper()
		value, err := app.store.durable(board.ID)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	createBody := func(title, tag string) string {
		return encode(map[string]string{
			"board_id": board.ID, "lane_id": board.LaneIDs[0],
			"expected_board_etag": tag, "title": title,
		})
	}
	assertRejected := func(raw []byte, before snapshot) {
		t.Helper()
		problem := turnOneDecode[problemBody](t, raw)
		if problem.Code != "invalid_request" || problem.Outcome != "not_applied" ||
			len(problem.Errors) != 1 || problem.Errors[0].Field != "title" {
			t.Fatalf("expected title validation problem, got %+v", problem)
		}
		if after := load(); !reflect.DeepEqual(before, after) {
			t.Fatal("rejected title changed the durable board, revision, or activity")
		}
		accepted, ok := app.store.accepted(board.ID)
		if !ok || !reflect.DeepEqual(before, accepted) {
			t.Fatal("rejected title changed the accepted board")
		}
	}
	blankTitles := []string{
		"", " ", "\t\r\n", "\v\f", "\u0085", "\u00a0", "\u1680",
		"\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a",
		"\u2028\u2029", "\u202f", "\u205f", "\u3000", " \t\u00a0\u2003\u3000\r\n",
	}
	for _, title := range blankTitles {
		t.Run("create_"+encode(title), func(t *testing.T) {
			before := load()
			raw, _ := client.call("POST", "/cards", createBody(title, before.Board.entityTag()), "", 400)
			assertRejected(raw, before)
		})
	}

	title := "\u00a0\t 🐝 e\u0301 漢字 \u3000\n"
	raw, _ = client.call("POST", "/cards", createBody(title, board.entityTag()), "", 201)
	created := turnOneDecode[card](t, raw)
	if created.Title != title {
		t.Fatalf("created title changed: %q", created.Title)
	}
	for _, title := range blankTitles {
		t.Run("patch_"+encode(title), func(t *testing.T) {
			before := load()
			body := encode(map[string]string{"title": title, "description": "Must not be applied"})
			raw, _ := client.call("PATCH", "/cards/"+created.ID, body, created.entityTag(), 400)
			assertRejected(raw, before)
		})
	}

	title = "\u3000 Updated 🐝 e\u0301 \u00a0\t"
	raw, _ = client.call("PATCH", "/cards/"+created.ID, encode(map[string]string{"title": title}), created.entityTag(), 200)
	updated := turnOneDecode[card](t, raw)
	raw, _ = client.call("GET", "/cards/"+created.ID, "", "", 200)
	readback := turnOneDecode[card](t, raw)
	if updated.Title != title || readback.Title != title || load().Cards[0].Title != title {
		t.Fatal("meaningful title did not preserve exact Unicode and surrounding whitespace")
	}

	valid := load()
	if err := validateSnapshot(valid, board.ID); err != nil {
		t.Fatalf("valid title snapshot rejected: %v", err)
	}
	for _, title := range blankTitles {
		t.Run("snapshot_"+encode(title), func(t *testing.T) {
			candidate := cloneSnapshot(valid)
			candidate.Cards[0].Title = title
			if err := validateSnapshot(candidate, board.ID); !errors.Is(err, errInvalidSnapshot) {
				t.Fatalf("blank title snapshot admission returned %v", err)
			}
		})
	}
}
