package restore

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	V "github.com/localswim/localswim/internal/archive/validate"
	A "github.com/localswim/localswim/internal/contract/archive"
)

const currentCover A.UUID = "00000000-0000-4000-8000-000000000031"
const historicalCover A.UUID = "00000000-0000-4000-8000-000000000032"

func coverRaster(t *testing.T, gray uint8) []byte {
	t.Helper()
	im := image.NewGray(image.Rect(0, 0, 1, 1))
	im.SetGray(0, 0, color.Gray{Y: gray})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func coverMembers(t *testing.T, current, historical []byte, change func(*A.Board, *A.History)) map[string][]byte {
	t.Helper()
	members := fixture(t)
	b, err := A.DecodeReadable[A.Board](members["board.json"])
	if err != nil {
		t.Fatal(err)
	}
	h, err := A.DecodeReadable[A.History](members["history.json"])
	if err != nil {
		t.Fatal(err)
	}
	add := func(id A.UUID, data []byte, at A.Timestamp) {
		digest := hash(data)
		members["blobs/sha256-"+string(digest)] = data
		b.Attachments = append(b.Attachments, A.Attachment{AttachmentID: id, CardID: b.Cards[0].CardID, Name: "image.bin", MediaLabel: "application/octet-stream", SHA256: digest, ByteLength: A.Number(len(data)), CreatedAt: at, CreatedIn: b.Identity})
	}
	add(currentCover, current, "2026-10-01T00:00:00Z")
	add(historicalCover, historical, h.Records[0].CommittedAt)
	chosen, retained := currentCover, historicalCover
	b.Cards[0].CoverAttachmentID = &chosen
	b.Cards[0].AttachmentIDs = append(b.Cards[0].AttachmentIDs, chosen)
	h.Records[0].State.CoverAttachmentID = &retained
	h.Records[0].References.AttachmentIDs = append(h.Records[0].References.AttachmentIDs, retained)
	if change != nil {
		change(&b, &h)
	}
	for name, value := range map[string]any{"board.json": b, "history.json": h} {
		members[name], err = A.Readable(value)
		if err != nil {
			t.Fatal(name, err)
		}
	}
	return members
}

func TestCoverArchiveChecksExactMembershipAndVerifiedRaster(t *testing.T) {
	current, historical := coverRaster(t, 32), coverRaster(t, 224)
	if _, err := prepare(context.Background(), sourcePackage(t, coverMembers(t, current, historical, nil)), testTime, testInc); err != nil {
		t.Fatal(err)
	}
	bad := []byte("valid digest and length, unsupported cover bytes")
	for _, tc := range []struct {
		name                string
		current, historical []byte
		change              func(*A.Board, *A.History)
	}{
		{"invalid current raster", bad, historical, nil},
		{"invalid historical raster", current, bad, nil},
		{"retained membership cannot select a current cover", current, historical, func(b *A.Board, h *A.History) {
			b.Cards[0].CoverAttachmentID = h.Records[0].State.CoverAttachmentID
		}},
		{"current membership cannot select a historical cover", current, historical, func(b *A.Board, h *A.History) {
			h.Records[0].State.CoverAttachmentID = b.Cards[0].CoverAttachmentID
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			members := coverMembers(t, tc.current, tc.historical, tc.change)
			if _, err := V.Read(context.Background(), bytes.NewReader(sourceArchiveBytes(t, members))); !errors.Is(err, V.ErrInvalid) {
				t.Fatal("bad cover archive accepted", err)
			}
		})
	}
}
