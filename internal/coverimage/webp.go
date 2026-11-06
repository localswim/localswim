package coverimage

import "encoding/binary"

func webp(data []byte) (uint32, uint32) {
	if len(data) < 12 || binary.LittleEndian.Uint32(data[4:8]) != uint32(len(data)-8) ||
		string(data[8:12]) != "WEBP" {
		return 0, 0
	}
	kind, chunk, rest, ok := webpChunk(data[12:])
	if !ok {
		return 0, 0
	}
	switch kind {
	case "VP8 ", "VP8L":
		if len(rest) == 0 {
			return webpRaster(kind, chunk)
		}
	case "VP8X":
		return webpExtended(chunk, rest)
	}
	return 0, 0
}

func webpChunk(data []byte) (kind string, chunk, rest []byte, ok bool) {
	if len(data) < 8 {
		return "", nil, nil, false
	}
	size := binary.LittleEndian.Uint32(data[4:8])
	// Compare before converting or adding any offsets.
	if size > uint32(len(data)-8) {
		return "", nil, nil, false
	}
	end := 8 + int(size)
	chunk, rest = data[8:end], data[end:]
	if size&1 != 0 {
		if len(rest) == 0 || rest[0] != 0 {
			return "", nil, nil, false
		}
		rest = rest[1:]
	}
	return string(data[:4]), chunk, rest, true
}

func webpExtended(header, data []byte) (uint32, uint32) {
	if len(header) != 10 || header[0]&0xc1 != 0 || littleUint24(header[1:4]) != 0 {
		return 0, 0
	}
	width, height := littleUint24(header[4:7])+1, littleUint24(header[7:10])+1
	if !admitted(width, height) {
		return 0, 0
	}
	if header[0]&0x02 == 0 {
		if webpStill(data, width, height, false) {
			return width, height
		}
		return 0, 0
	}

	seenAnimation, seenFrame := false, false
	for len(data) > 0 {
		kind, chunk, rest, ok := webpChunk(data)
		if !ok {
			return 0, 0
		}
		switch kind {
		case "ANIM":
			if seenAnimation || len(chunk) != 6 {
				return 0, 0
			}
			seenAnimation = true
		case "ANMF":
			if !seenAnimation || !webpFrame(chunk, width, height) {
				return 0, 0
			}
			seenFrame = true
		case "VP8 ", "VP8L", "VP8X", "ALPH":
			return 0, 0
		}
		data = rest
	}
	if !seenFrame {
		return 0, 0
	}
	return width, height
}

func webpFrame(data []byte, canvasWidth, canvasHeight uint32) bool {
	if len(data) < 16 || data[15]&0xfc != 0 {
		return false
	}
	x, y := 2*littleUint24(data[:3]), 2*littleUint24(data[3:6])
	width, height := littleUint24(data[6:9])+1, littleUint24(data[9:12])+1
	return fits(x, y, width, height, canvasWidth, canvasHeight) &&
		webpStill(data[16:], width, height, true)
}

// webpStill validates one raster stream contained by an extended canvas or frame.
func webpStill(data []byte, width, height uint32, frame bool) bool {
	seenRaster, seenAlpha := false, false
	for len(data) > 0 {
		kind, chunk, rest, ok := webpChunk(data)
		if !ok {
			return false
		}
		switch kind {
		case "VP8 ", "VP8L":
			w, h := webpRaster(kind, chunk)
			if seenRaster || (seenAlpha && kind == "VP8L") || w != width || h != height {
				return false
			}
			seenRaster = true
		case "ALPH":
			if seenAlpha || seenRaster || len(chunk) == 0 || chunk[0]&0xc0 != 0 ||
				chunk[0]&3 > 1 || (chunk[0]>>4)&3 > 1 {
				return false
			}
			seenAlpha = true
		case "VP8X", "ANMF":
			return false
		case "ANIM":
			// A still image ignores a top-level animation control chunk.
			if frame {
				return false
			}
		}
		data = rest
	}
	return seenRaster
}

func webpRaster(kind string, data []byte) (uint32, uint32) {
	if kind == "VP8L" {
		if len(data) < 5 || data[0] != 0x2f {
			return 0, 0
		}
		bits := binary.LittleEndian.Uint32(data[1:5])
		if bits>>29 != 0 { // Only bitstream version zero is defined.
			return 0, 0
		}
		return (bits & 0x3fff) + 1, ((bits >> 14) & 0x3fff) + 1
	}
	if len(data) < 10 || data[0]&1 != 0 || data[0]&0x10 == 0 || (data[0]>>1)&7 > 3 ||
		string(data[3:6]) != "\x9d\x01\x2a" || littleUint24(data[:3])>>5 > uint32(len(data)-10) {
		return 0, 0
	}
	return uint32(binary.LittleEndian.Uint16(data[6:8]) & 0x3fff),
		uint32(binary.LittleEndian.Uint16(data[8:10]) & 0x3fff)
}

func littleUint24(data []byte) uint32 {
	return uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16
}
