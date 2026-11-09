//go:build windows && amd64

package installation

import (
	"runtime"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

var compareStringOrdinal = windows.NewLazySystemDLL("kernel32.dll").NewProc("CompareStringOrdinal")

// Use the OS ordinal uppercase table for native path identity. Go Unicode case
// folding can alias names that Windows keeps distinct (e.g. K and Kelvin sign).
// https://learn.microsoft.com/windows/win32/api/stringapiset/nf-stringapiset-comparestringordinal
func ordinalPathEqual(left, right string) (bool, error) {
	if left == "" || right == "" || !utf8.ValidString(left) || !utf8.ValidString(right) || compareStringOrdinal.Find() != nil {
		return false, ErrUnavailable
	}
	a, err := windows.UTF16FromString(left)
	if err != nil || len(a) > 4097 {
		return false, ErrUnavailable
	}
	b, err := windows.UTF16FromString(right)
	if err != nil || len(b) > 4097 {
		return false, ErrUnavailable
	}
	value, _, _ := compareStringOrdinal.Call(uintptr(unsafe.Pointer(&a[0])), uintptr(len(a)-1), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)-1), 1)
	runtime.KeepAlive(a)
	runtime.KeepAlive(b)
	switch value {
	case 1, 3:
		return false, nil
	case 2:
		return true, nil
	default:
		return false, ErrUnavailable
	}
}

// Native names must survive UTF-16-to-Go conversion exactly. An unpaired
// surrogate must not become a replacement character used to open another path.
func validPathUTF16(units []uint16) bool {
	if len(units) == 0 {
		return false
	}
	for i := 0; i < len(units); i++ {
		u := units[i]
		switch {
		case u == 0:
			return false
		case u >= 0xd800 && u <= 0xdbff:
			i++
			if i >= len(units) || units[i] < 0xdc00 || units[i] > 0xdfff {
				return false
			}
		case u >= 0xdc00 && u <= 0xdfff:
			return false
		}
	}
	return true
}

func (b *candidateBinding) chooseNative(path, key string) error {
	if b.nativePath != "" {
		same, err := ordinalPathEqual(b.nativePath, path)
		if err != nil {
			return err
		}
		if !same {
			return ErrIdentityChanged
		}
	} else if _, err := ordinalPathEqual(path, path); err != nil {
		return err
	}
	if err := b.choose(key); err != nil {
		return err
	}
	if b.nativePath == "" {
		b.nativePath = path
	}
	return nil
}
