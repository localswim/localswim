package v2

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"runtime"
	"strconv"
)

func nativeBuildRegion(data []byte) []byte {
	switch runtime.GOOS {
	case "windows":
		return peBuildRegion(data)
	case "darwin":
		return machoBuildRegion(data)
	default:
		return elfBuildRegion(data)
	}
}

// imageRange checks bounds before converting image-supplied values to indexes.
func imageRange(data []byte, offset, length uint64) []byte {
	if offset > uint64(len(data)) || length > uint64(len(data))-offset {
		return nil
	}
	return data[offset : offset+length]
}

func peBuildRegion(data []byte) []byte {
	if len(data) < 64 || string(data[:2]) != "MZ" {
		return nil
	}
	order := binary.LittleEndian
	offset := uint64(order.Uint32(data[60:]))
	coff := imageRange(data, offset, 24)
	if offset < 64 || coff == nil || string(coff[:4]) != "PE\x00\x00" {
		return nil
	}
	var machine uint16
	var optionalMagic uint16 = 0x20b // PE32+
	var minOptionalSize uint64 = 112
	switch runtime.GOARCH {
	case "386":
		machine, optionalMagic, minOptionalSize = pe.IMAGE_FILE_MACHINE_I386, 0x10b, 96
	case "amd64":
		machine = pe.IMAGE_FILE_MACHINE_AMD64
	case "arm64":
		machine = pe.IMAGE_FILE_MACHINE_ARM64
	default:
		return nil
	}
	count, flags := order.Uint16(coff[6:]), order.Uint16(coff[22:])
	if order.Uint16(coff[4:]) != machine || count == 0 || count > 96 ||
		flags&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || flags&pe.IMAGE_FILE_DLL != 0 {
		return nil
	}
	optionalSize := uint64(order.Uint16(coff[20:]))
	optional := imageRange(data, offset+24, optionalSize)
	if optional == nil || optionalSize < minOptionalSize || order.Uint16(optional) != optionalMagic {
		return nil
	}
	entry, imageSize, headerSize := order.Uint32(optional[16:]), order.Uint32(optional[56:]), order.Uint32(optional[60:])
	headers := imageRange(data, 0, uint64(headerSize))
	if headers == nil || entry < headerSize || entry >= imageSize {
		return nil
	}
	sections := imageRange(headers, offset+24+optionalSize, uint64(count)*40)
	if sections == nil {
		return nil
	}

	const initializedData = pe.IMAGE_SCN_CNT_INITIALIZED_DATA | pe.IMAGE_SCN_MEM_READ | pe.IMAGE_SCN_MEM_WRITE
	const executableCode = pe.IMAGE_SCN_CNT_CODE | pe.IMAGE_SCN_MEM_EXECUTE
	var region []byte
	var backedEntry bool
	for section := range int(count) {
		row := sections[section*40:]
		virtualSize, address := order.Uint32(row[8:]), order.Uint32(row[12:])
		size, start := order.Uint32(row[16:]), order.Uint32(row[20:])
		if size == 0 {
			continue
		}
		raw := imageRange(data, uint64(start), uint64(size))
		if start < headerSize || raw == nil {
			return nil
		}
		if address < headerSize || address >= imageSize || virtualSize > imageSize-address {
			return nil
		}
		flags := order.Uint32(row[36:])
		mappedSize := min(virtualSize, size)
		if flags&executableCode == executableCode && entry >= address && entry-address < mappedSize {
			backedEntry = true
		}
		// Match the Go toolchain's first writable, initialized data section.
		const align32 = 0x00600000
		if region == nil && mappedSize > 0 && flags&^align32 == initializedData {
			region = raw[:mappedSize]
		}
	}
	if !backedEntry {
		return nil
	}
	return region
}

func nativeELFOrder(data []byte) binary.ByteOrder {
	var machine elf.Machine
	switch runtime.GOARCH {
	case "386":
		machine = elf.EM_386
	case "amd64":
		machine = elf.EM_X86_64
	case "arm":
		machine = elf.EM_ARM
	case "arm64":
		machine = elf.EM_AARCH64
	case "loong64":
		machine = elf.EM_LOONGARCH
	case "mips", "mipsle", "mips64", "mips64le":
		machine = elf.EM_MIPS
	case "ppc64", "ppc64le":
		machine = elf.EM_PPC64
	case "riscv64":
		machine = elf.EM_RISCV
	case "s390x":
		machine = elf.EM_S390
	default:
		return nil
	}
	class := elf.ELFCLASS32
	if strconv.IntSize == 64 {
		class = elf.ELFCLASS64
	}
	encoding := elf.ELFDATA2MSB
	if binary.NativeEndian.Uint16([]byte{1, 0}) == 1 {
		encoding = elf.ELFDATA2LSB
	}
	if elf.Class(data[4]) != class || elf.Data(data[5]) != encoding ||
		elf.Machine(binary.NativeEndian.Uint16(data[18:])) != machine {
		return nil
	}
	return binary.NativeEndian
}
func elfBuildRegion(data []byte) []byte {
	if len(data) < 52 || string(data[:4]) != "\x7fELF" {
		return nil
	}
	order := nativeELFOrder(data)
	if order == nil {
		return nil
	}
	kind := elf.Type(order.Uint16(data[16:]))
	if kind != elf.ET_EXEC && kind != elf.ET_DYN {
		return nil
	}
	var entry, tableOffset, rowSize uint64
	var size, count uint16
	is64 := elf.Class(data[4]) == elf.ELFCLASS64
	switch elf.Class(data[4]) {
	case elf.ELFCLASS32:
		entry, tableOffset = uint64(order.Uint32(data[24:])), uint64(order.Uint32(data[28:]))
		size, count, rowSize = order.Uint16(data[42:]), order.Uint16(data[44:]), 32
	case elf.ELFCLASS64:
		if len(data) < 64 {
			return nil
		}
		entry, tableOffset = order.Uint64(data[24:]), order.Uint64(data[32:])
		size, count, rowSize = order.Uint16(data[54:]), order.Uint16(data[56:]), 56
	default:
		return nil
	}
	table := imageRange(data, tableOffset, uint64(count)*rowSize)
	if entry == 0 || table == nil || count == 0 || count > 256 || uint64(size) != rowSize {
		return nil
	}
	var region []byte
	var backedEntry bool
	for segment := range uint64(count) {
		row := table[segment*rowSize:]
		if elf.ProgType(order.Uint32(row)) != elf.PT_LOAD {
			continue
		}
		var offset, address, filesz, memsz uint64
		var flags elf.ProgFlag
		if is64 {
			flags = elf.ProgFlag(order.Uint32(row[4:]))
			offset, address = order.Uint64(row[8:]), order.Uint64(row[16:])
			filesz, memsz = order.Uint64(row[32:]), order.Uint64(row[40:])
		} else {
			offset, address = uint64(order.Uint32(row[4:])), uint64(order.Uint32(row[8:]))
			filesz, memsz = uint64(order.Uint32(row[16:])), uint64(order.Uint32(row[20:]))
			flags = elf.ProgFlag(order.Uint32(row[24:]))
		}
		raw := imageRange(data, offset, filesz)
		if raw == nil || filesz > memsz {
			return nil
		}
		if flags&elf.PF_X != 0 && entry >= address && entry-address < filesz {
			backedEntry = true
		}
		if region == nil && len(raw) > 0 && flags&(elf.PF_R|elf.PF_W|elf.PF_X) == elf.PF_R|elf.PF_W {
			region = raw
		}
	}
	if !backedEntry {
		return nil
	}
	return region
}

func nativeMachHeader(data []byte) bool {
	var cpu macho.Cpu
	switch runtime.GOARCH {
	case "amd64":
		cpu = macho.CpuAmd64
	case "arm64":
		cpu = macho.CpuArm64
	default:
		return false
	}
	order := binary.LittleEndian
	return len(data) >= 32 && order.Uint32(data) == macho.Magic64 &&
		macho.Cpu(order.Uint32(data[4:])) == cpu &&
		macho.Type(order.Uint32(data[12:])) == macho.TypeExec
}
func machoBuildRegion(data []byte) []byte {
	const lcMain = 0x80000028
	order := binary.LittleEndian
	if !nativeMachHeader(data) {
		return nil
	}
	count, size := order.Uint32(data[16:]), order.Uint32(data[20:])
	commands := imageRange(data, 32, uint64(size))
	if commands == nil || count == 0 || count > 256 {
		return nil
	}
	var region []byte
	var entry, codeStart, codeSize uint64
	for range count {
		if len(commands) < 8 {
			return nil
		}
		commandSize := uint64(order.Uint32(commands[4:]))
		command := imageRange(commands, 0, commandSize)
		if command == nil || len(command) < 8 {
			return nil
		}
		commands = commands[commandSize:]
		switch order.Uint32(command) {
		case lcMain:
			if len(command) < 24 {
				return nil
			}
			entry = order.Uint64(command[8:])
		case uint32(macho.LoadCmdSegment64):
			if len(command) < 72 {
				return nil
			}
			start, size := order.Uint64(command[40:]), order.Uint64(command[48:])
			sections := order.Uint32(command[64:])
			if imageRange(data, start, size) == nil || uint64(sections)*80 != uint64(len(command)-72) {
				return nil
			}
			const write = 2
			const execute = 4
			flags := order.Uint32(command[60:])
			for section := range uint64(sections) {
				row := command[72+section*80:]
				name := bytes.TrimRight(row[:16], "\x00")
				offset, length := uint64(order.Uint32(row[48:])), order.Uint64(row[40:])
				switch string(name) {
				case "__text":
					if flags&execute != 0 && start == 0 && offset <= size && length <= size-offset {
						codeStart, codeSize = offset, length
					}
				case "__go_buildinfo":
					if flags&(write|execute) == write && offset >= start && offset-start <= size && length <= size-(offset-start) {
						region = imageRange(data, offset, length)
					}
				}
			}
		}
	}
	if len(commands) != 0 || entry == 0 || entry < codeStart || entry-codeStart >= codeSize {
		return nil
	}
	return region
}
