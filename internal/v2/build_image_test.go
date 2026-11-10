package v2

import (
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"runtime"
	"testing"
)

func TestBuildUnixHeadersRequireNativeTarget(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("test fixtures use amd64")
	}
	t.Run("ELF", func(t *testing.T) {
		header := make([]byte, 64)
		header[4], header[5] = byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB)
		binary.LittleEndian.PutUint16(header[18:], uint16(elf.EM_X86_64))
		if nativeELFOrder(header) == nil {
			t.Fatal("refused native ELF target")
		}
		for _, field := range []int{4, 5, 18} {
			foreign := append([]byte(nil), header...)
			foreign[field] ^= 1
			if nativeELFOrder(foreign) != nil {
				t.Errorf("accepted ELF with changed target field at byte %d", field)
			}
		}
	})
	t.Run("Mach-O", func(t *testing.T) {
		header := make([]byte, 32)
		binary.LittleEndian.PutUint32(header, macho.Magic64)
		binary.LittleEndian.PutUint32(header[4:], uint32(macho.CpuAmd64))
		binary.LittleEndian.PutUint32(header[12:], uint32(macho.TypeExec))
		if !nativeMachHeader(header) {
			t.Fatal("refused native Mach-O target")
		}
		binary.LittleEndian.PutUint32(header[4:], uint32(macho.CpuArm64))
		if nativeMachHeader(header) {
			t.Fatal("accepted foreign Mach-O CPU")
		}
	})
}
