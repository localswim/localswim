package coverimage

import "encoding/binary"

func gif(data []byte) (uint32, uint32) {
	if len(data) < 7 {
		return 0, 0
	}
	width := gifUint16(data)
	height := gifUint16(data[2:])
	if !admitted(width, height) {
		return 0, 0
	}
	data = gifColorTable(data[7:], data[4])
	seenFrame := false
	for len(data) > 0 {
		switch data[0] {
		case 0x21: // Extension.
			data = gifExtension(data[1:])
		case 0x2c: // Image descriptor.
			data = gifFrame(data[1:], width, height)
			seenFrame = true
		case 0x3b: // Trailer.
			if len(data) == 1 && seenFrame {
				return width, height
			}
			return 0, 0
		default:
			return 0, 0
		}
	}
	return 0, 0
}

func gifUint16(data []byte) uint32 {
	return uint32(binary.LittleEndian.Uint16(data))
}

func gifColorTable(data []byte, flags byte) []byte {
	if flags&0x80 == 0 {
		return data
	}
	size := 3 * (2 << (flags & 7))
	if len(data) < size {
		return nil
	}
	return data[size:]
}

func gifFrame(data []byte, width, height uint32) []byte {
	if len(data) < 9 || data[8]&0x18 != 0 {
		return nil
	}
	x, y := gifUint16(data), gifUint16(data[2:])
	w, h := gifUint16(data[4:]), gifUint16(data[6:])
	if !fits(x, y, w, h, width, height) {
		return nil
	}
	data = gifColorTable(data[9:], data[8])
	if len(data) < 2 || data[0] < 2 || data[0] > 8 || data[1] == 0 {
		return nil
	}
	return gifSubBlocks(data[1:])
}

func gifExtension(data []byte) []byte {
	if len(data) < 2 {
		return nil
	}
	switch data[0] {
	case 0xf9: // Graphic control has one four-byte block.
		if len(data) < 7 || data[1] != 4 || data[6] != 0 {
			return nil
		}
		return data[7:]
	case 0x01: // Plain text header.
		if data[1] != 12 {
			return nil
		}
	case 0xff: // Application header.
		if data[1] != 11 {
			return nil
		}
	}
	return gifSubBlocks(data[1:])
}

func gifSubBlocks(data []byte) []byte {
	for len(data) > 0 {
		size := int(data[0])
		if size == 0 {
			return data[1:]
		}
		if size >= len(data) {
			return nil
		}
		data = data[1+size:]
	}
	return nil
}

func fits(x, y, width, height, canvasWidth, canvasHeight uint32) bool {
	return width > 0 && height > 0 && x < canvasWidth && y < canvasHeight &&
		width <= canvasWidth-x && height <= canvasHeight-y
}
