package validate

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"sort"
	"strings"
)

func u16(b []byte, n int) uint16 { return binary.LittleEndian.Uint16(b[n:]) }
func u32(b []byte, n int) uint32 { return binary.LittleEndian.Uint32(b[n:]) }

type header struct {
	name                               string
	version, flags, method, time, date uint16
	crc, size, offset                  uint32
}

// parseZIP deliberately parses both header sets. archive/zip alone accepts
// preambles, trailing bytes and local metadata disagreements. All offsets are
// checked against the actual bounded input before slicing or payload decoding.
func parseZIP(ctx context.Context, raw []byte) ([]member, error) {
	if len(raw) > MaxArchiveBytes {
		return nil, ErrLimit
	}
	if len(raw) < 22 {
		return nil, ErrInvalid
	}
	// EOCD comments may contain signature bytes. Try every length-consistent
	// candidate, including an earlier genuine EOCD before such a comment.
	first := len(raw) - 22 - 65535
	if first < 0 {
		first = 0
	}
	limit := false
	for end := first; end <= len(raw)-22; end++ {
		if u32(raw, end) != 0x06054b50 || end+22+int(u16(raw, end+20)) != len(raw) {
			continue
		}
		ms, err := parseZIPAt(ctx, raw, end)
		if err == nil {
			return ms, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		limit = limit || err == ErrLimit
	}
	if limit {
		return nil, ErrLimit
	}
	return nil, ErrInvalid
}

func parseZIPAt(ctx context.Context, raw []byte, end int) ([]member, error) {
	e := raw[end:]
	if u16(e, 4) != 0 || u16(e, 6) != 0 {
		return nil, ErrInvalid
	}
	count := int(u16(e, 10))
	if count > MaxMembers {
		return nil, ErrLimit
	}
	if count < 6 || int(u16(e, 8)) != count {
		return nil, ErrInvalid
	}
	start, length := uint64(u32(e, 16)), uint64(u32(e, 12))
	if start+length != uint64(end) || start > uint64(end) {
		return nil, ErrInvalid
	}
	pos := int(start)
	hs := make([]header, 0, count)
	seen := make(map[string]bool, count)
	var jsonTotal, blobTotal uint64
	for i := 0; i < count; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pos > end-46 {
			return nil, ErrInvalid
		}
		c := raw[pos:]
		if u32(c, 0) != 0x02014b50 {
			return nil, ErrInvalid
		}
		nl, xl, cl := int(u16(c, 28)), int(u16(c, 30)), int(u16(c, 32))
		if nl+xl+cl > end-pos-46 || u16(c, 34) != 0 || u16(c, 36)&^uint16(1) != 0 {
			return nil, ErrInvalid
		}
		if !extrasAt(c[46+nl:46+nl+xl], true) {
			return nil, ErrInvalid
		}
		name := string(c[46 : 46+nl])
		if !safeName(name) || seen[name] {
			return nil, ErrInvalid
		}
		seen[name] = true
		h := header{name, u16(c, 6), u16(c, 8), u16(c, 10), u16(c, 12), u16(c, 14), u32(c, 16), u32(c, 24), u32(c, 42)}
		if (h.version != 10 && h.version != 20) || (h.flags != 0 && h.flags != 0x800) || h.method != 0 || u32(c, 20) != h.size || h.size == 0xffffffff || h.offset == 0xffffffff {
			return nil, ErrInvalid
		}
		if !attributes(u16(c, 4), u32(c, 38)) {
			return nil, ErrInvalid
		}
		hs = append(hs, h)
		pos += 46 + nl + xl + cl
	}
	if pos != end {
		return nil, ErrInvalid
	}
	// Central listing order is not the physical member order. Offsets determine
	// that order; container metadata and timestamps have no canonical byte form.
	sort.Slice(hs, func(i, j int) bool { return hs[i].offset < hs[j].offset })
	for i, h := range hs {
		name := h.name
		if i < 5 {
			if name != [...]string{"manifest.json", "board.json", "history.json", "activity.json", "outcomes.json"}[i] {
				return nil, ErrInvalid
			}
			if err := budget(&jsonTotal, uint64(h.size), MaxJSONBytes, MaxJSONTotalBytes); err != nil {
				return nil, err
			}
		} else if i == count-1 {
			if name != "COMPLETE" || h.size != 65 {
				return nil, ErrInvalid
			}
		} else {
			if !strings.HasPrefix(name, "blobs/sha256-") || (i > 5 && hs[i-1].name >= name) {
				return nil, ErrInvalid
			}
			if err := budget(&blobTotal, uint64(h.size), MaxBlobBytes, MaxBlobTotalBytes); err != nil {
				return nil, err
			}
		}
	}
	ms := make([]member, 0, count)
	pos = 0
	for _, h := range hs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if uint64(pos) != uint64(h.offset) || uint64(pos)+30 > start {
			return nil, ErrInvalid
		}
		l := raw[pos:]
		nl, xl := int(u16(l, 26)), int(u16(l, 28))
		dataStart := uint64(pos) + 30 + uint64(nl) + uint64(xl)
		dataEnd := dataStart + uint64(h.size)
		if u32(l, 0) != 0x04034b50 || dataEnd > start || dataStart > start {
			return nil, ErrInvalid
		}
		if !extras(l[30+nl : 30+nl+xl]) {
			return nil, ErrInvalid
		}
		if !bytes.Equal(l[30:30+nl], []byte(h.name)) || (u16(l, 4) != 10 && u16(l, 4) != 20) || u16(l, 6) != h.flags || u16(l, 8) != h.method || u32(l, 14) != h.crc || u32(l, 18) != h.size || u32(l, 22) != h.size {
			return nil, ErrInvalid
		}
		data := raw[int(dataStart):int(dataEnd)]
		if crc32.ChecksumIEEE(data) != h.crc {
			return nil, ErrInvalid
		}
		ms = append(ms, member{h.name, data})
		pos = int(dataEnd)
	}
	if uint64(pos) != start {
		return nil, ErrInvalid
	}
	return ms, nil
}

// Only timestamp-only extra records are supported. Their values remain inert;
// they cannot override names, sizes, encoding, link kind or storage method.
func extras(b []byte) bool {
	return extrasAt(b, false)
}

func extrasAt(b []byte, central bool) bool {
	seen := map[uint16]bool{}
	for len(b) > 0 {
		if len(b) < 4 {
			return false
		}
		tag, n := u16(b, 0), int(u16(b, 2))
		if n > len(b)-4 || seen[tag] {
			return false
		}
		seen[tag] = true
		x := b[4 : 4+n]
		switch tag {
		case 0x5455: // Extended timestamp: flags followed by selected Unix times.
			if len(x) < 1 || x[0]&^byte(7) != 0 {
				return false
			}
			count := 0
			for bit := byte(1); bit <= 4; bit <<= 1 {
				if x[0]&bit != 0 {
					count++
				}
			}
			// The central form may omit access/creation values even when the
			// corresponding flags describe values in the local extra record.
			centralLength := 1
			if x[0]&1 != 0 {
				centralLength += 4
			}
			if len(x) != 1+4*count && !(central && len(x) == centralLength) {
				return false
			}
		case 0x000a: // NTFS: reserved zero, one timestamp attribute, three FILETIMEs.
			if len(x) != 32 || u32(x, 0) != 0 || u16(x, 4) != 1 || u16(x, 6) != 24 {
				return false
			}
		default:
			return false
		}
		b = b[4+n:]
	}
	return true
}

func budget(total *uint64, n, each, maximum uint64) error {
	if n > each || *total > maximum || n > maximum-*total {
		return ErrLimit
	}
	*total += n
	return nil
}

func safeName(s string) bool {
	switch s {
	case "manifest.json", "board.json", "history.json", "activity.json", "outcomes.json", "COMPLETE":
		return true
	}
	if len(s) != len("blobs/sha256-")+64 || !strings.HasPrefix(s, "blobs/sha256-") {
		return false
	}
	for _, b := range []byte(s[len("blobs/sha256-"):]) {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
	}
	return true
}

func attributes(creator uint16, external uint32) bool {
	system := creator >> 8
	// DOS permits only the inert read-only/hidden/system/archive bits. Reject
	// directory, volume-label, device/reserved bits and all unrecognized systems.
	if external&0xffff & ^uint32(0x27) != 0 {
		return false
	}
	if system == 0 {
		return external>>16 == 0
	}
	if system != 3 {
		return false
	}
	mode := external >> 16
	return mode&0170000 == 0100000 && mode & ^uint32(0170777) == 0
}
