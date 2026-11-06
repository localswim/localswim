package coverimage

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	stdgif "image/gif"
	stdjpeg "image/jpeg"
	stdpng "image/png"
	"os"
	"testing"
)

func TestAdmissionParity(t *testing.T) {
	var vectors []struct {
		Name             string
		Base64           string
		ZeroPaddedLength int
		Header           *Header
	}
	fixture, err := os.ReadFile("testdata/raster-admission.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixture, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			data, err := base64.StdEncoding.Strict().DecodeString(vector.Base64)
			if err != nil {
				t.Fatal(err)
			}
			if vector.ZeroPaddedLength != 0 {
				data = append(data, make([]byte, vector.ZeroPaddedLength-len(data))...)
			}
			got, ok := Inspect(data)
			if vector.Header == nil {
				if ok || got != (Header{}) {
					t.Fatalf("expected rejection, got %+v, %t", got, ok)
				}
			} else if !ok || got != *vector.Header {
				t.Fatalf("expected %+v, got %+v, %t", *vector.Header, got, ok)
			}
		})
	}
}

func TestAdmissionEncodedImages(t *testing.T) {
	img := image.NewPaletted(image.Rect(0, 0, 4, 3), color.Palette{color.Black, color.White})
	img.SetColorIndex(1, 1, 1)
	encoders := map[string]func(*bytes.Buffer) error{
		"image/png":  func(out *bytes.Buffer) error { return stdpng.Encode(out, img) },
		"image/jpeg": func(out *bytes.Buffer) error { return stdjpeg.Encode(out, img, nil) },
		"image/gif": func(out *bytes.Buffer) error {
			frame := image.NewPaletted(image.Rect(2, 1, 4, 3), img.Palette)
			return stdgif.EncodeAll(out, &stdgif.GIF{Image: []*image.Paletted{img, frame}, Delay: []int{4, 4}})
		},
	}
	for mime, encode := range encoders {
		t.Run(mime, func(t *testing.T) {
			var out bytes.Buffer
			if err := encode(&out); err != nil {
				t.Fatal(err)
			}
			header, ok := Inspect(out.Bytes())
			want := Header{MIME: mime, Width: 4, Height: 3}
			if !ok || header != want {
				t.Fatalf("expected %+v, got %+v, %t", want, header, ok)
			}
		})
	}
}
