// Package coverimage admits bounded raster headers for card covers.
package coverimage

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
)

const (
	MaxBytes  = 10 * 1024 * 1024
	maxAxis   = 4096
	maxPixels = 4194304

	pngSignature = "\x89PNG\r\n\x1a\n"
)

// Header contains only the admitted MIME type and encoded canvas dimensions.
type Header struct {
	MIME   string
	Width  uint32
	Height uint32
}

// Eligible reports whether verified attachment bytes meet the cover limits.
func Eligible(data []byte) bool {
	_, ok := Inspect(data)
	return ok
}

// Inspect classifies complete, already length- and digest-verified attachment
// bytes. It inspects the required structure without decoding compressed pixels.
func Inspect(data []byte) (Header, bool) {
	if len(data) == 0 || len(data) > MaxBytes {
		return Header{}, false
	}
	var mime string
	var width, height uint32
	switch {
	case bytes.HasPrefix(data, []byte(pngSignature)):
		mime = "image/png"
		width, height = png(data)
	case bytes.HasPrefix(data, []byte{0xff, 0xd8}):
		mime = "image/jpeg"
		width, height = jpeg(data[2:])
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		mime = "image/gif"
		width, height = gif(data[6:])
	case bytes.HasPrefix(data, []byte("RIFF")):
		mime = "image/webp"
		width, height = webp(data)
	}
	if !admitted(width, height) {
		return Header{}, false
	}
	return Header{MIME: mime, Width: width, Height: height}, true
}

func admitted(width, height uint32) bool {
	return width > 0 && height > 0 && width <= maxAxis && height <= maxAxis &&
		width*height <= maxPixels // Both factors are already bounded.
}

func png(data []byte) (uint32, uint32) {
	if len(data) < 33 || binary.BigEndian.Uint32(data[8:12]) != 13 || string(data[12:16]) != "IHDR" {
		return 0, 0
	}
	header := data[16:29]
	if crc32.ChecksumIEEE(data[12:29]) != binary.BigEndian.Uint32(data[29:33]) ||
		header[10] != 0 || header[11] != 0 || header[12] > 1 {
		return 0, 0
	}
	depth, color := header[8], header[9]
	switch color {
	case 0:
		if depth != 1 && depth != 2 && depth != 4 && depth != 8 && depth != 16 {
			return 0, 0
		}
	case 3:
		if depth != 1 && depth != 2 && depth != 4 && depth != 8 {
			return 0, 0
		}
	case 2, 4, 6:
		if depth != 8 && depth != 16 {
			return 0, 0
		}
	default:
		return 0, 0
	}
	return binary.BigEndian.Uint32(header), binary.BigEndian.Uint32(header[4:])
}

func jpeg(data []byte) (uint32, uint32) {
	for len(data) > 0 && data[0] == 0xff {
		// Repeated 0xff bytes are legal fill before a marker.
		for len(data) > 0 && data[0] == 0xff {
			data = data[1:]
		}
		if len(data) < 3 {
			break
		}
		marker := data[0]
		size := int(binary.BigEndian.Uint16(data[1:3]))
		if size < 2 || size > len(data)-1 {
			break
		}
		switch marker {
		case 0xc0, 0xc1, 0xc2: // Baseline, extended sequential, progressive DCT.
			return jpegFrame(marker, data[3:1+size])
		case 0xc4, 0xdb, 0xdd, 0xfe: // Huffman, quantization, restart interval, comment.
		default:
			if marker < 0xe0 || marker > 0xef { // APP0 to APP15.
				return 0, 0
			}
		}
		data = data[1+size:]
	}
	return 0, 0
}

func jpegFrame(marker byte, frame []byte) (uint32, uint32) {
	if len(frame) < 6 || frame[5] == 0 || len(frame) != 6+3*int(frame[5]) {
		return 0, 0
	}
	if frame[0] != 8 && (marker == 0xc0 || frame[0] != 12) {
		return 0, 0
	}
	var seen [256]bool
	for component := frame[6:]; len(component) > 0; component = component[3:] {
		id, horizontal, vertical := component[0], component[1]>>4, component[1]&15
		if seen[id] || horizontal < 1 || horizontal > 4 || vertical < 1 || vertical > 4 || component[2] > 3 {
			return 0, 0
		}
		seen[id] = true
	}
	return uint32(binary.BigEndian.Uint16(frame[3:])), uint32(binary.BigEndian.Uint16(frame[1:]))
}
