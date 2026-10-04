package rocmarch

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeELF writes a minimal ELF64 shared object holding, optionally, the
// ds4_rocm_offload_archs symbol (as .dynsym + .rodata) and a .hip_fatbin
// section. It stands in for a real libds4 without needing a C compiler.
func writeELF(t *testing.T, archSymbol *string, fatbin []byte) string {
	t.Helper()
	type section struct {
		name             string
		typ              uint32
		flags, addr      uint64
		data             []byte
		link, info       uint32
		entsize, nameOff uint64
	}
	secs := []*section{{}} // SHN_UNDEF
	add := func(s *section) uint16 { secs = append(secs, s); return uint16(len(secs) - 1) }

	if archSymbol != nil {
		rodata := append([]byte("pad!"), append([]byte(*archSymbol), 0)...)
		rodataIdx := add(&section{name: ".rodata", typ: 1 /* SHT_PROGBITS */, flags: 2, addr: 0x1000, data: rodata})
		dynstr := append([]byte{0}, append([]byte(ArchSymbol), 0)...)
		dynstrIdx := add(&section{name: ".dynstr", typ: 3, data: dynstr})
		var sym bytes.Buffer
		sym.Write(make([]byte, 24))                                         // null symbol
		binary.Write(&sym, binary.LittleEndian, uint32(1))                  // st_name
		sym.WriteByte(1<<4 | 1)                                             // STB_GLOBAL, STT_OBJECT
		sym.WriteByte(0)                                                    // st_other
		binary.Write(&sym, binary.LittleEndian, rodataIdx)                  // st_shndx
		binary.Write(&sym, binary.LittleEndian, uint64(0x1004))             // st_value
		binary.Write(&sym, binary.LittleEndian, uint64(len(*archSymbol)+1)) // st_size
		add(&section{name: ".dynsym", typ: 11, data: sym.Bytes(), link: uint32(dynstrIdx), info: 1, entsize: 24})
	}
	if fatbin != nil {
		add(&section{name: ".hip_fatbin", typ: 1, flags: 2, addr: 0x8000, data: fatbin})
	}
	var shstr bytes.Buffer
	shstr.WriteByte(0)
	shstrIdx := add(&section{name: ".shstrtab", typ: 3})
	for _, s := range secs[1:] {
		s.nameOff = uint64(shstr.Len())
		shstr.WriteString(s.name)
		shstr.WriteByte(0)
	}
	secs[shstrIdx].data = shstr.Bytes()

	var body bytes.Buffer
	body.Write(make([]byte, 64)) // ELF header, filled below
	offsets := make([]uint64, len(secs))
	for i, s := range secs[1:] {
		for body.Len()%8 != 0 {
			body.WriteByte(0)
		}
		offsets[i+1] = uint64(body.Len())
		body.Write(s.data)
	}
	for body.Len()%8 != 0 {
		body.WriteByte(0)
	}
	shoff := uint64(body.Len())
	for i, s := range secs {
		binary.Write(&body, binary.LittleEndian, uint32(s.nameOff))
		binary.Write(&body, binary.LittleEndian, s.typ)
		binary.Write(&body, binary.LittleEndian, s.flags)
		binary.Write(&body, binary.LittleEndian, s.addr)
		binary.Write(&body, binary.LittleEndian, offsets[i])
		binary.Write(&body, binary.LittleEndian, uint64(len(s.data)))
		binary.Write(&body, binary.LittleEndian, s.link)
		binary.Write(&body, binary.LittleEndian, s.info)
		binary.Write(&body, binary.LittleEndian, uint64(1))
		binary.Write(&body, binary.LittleEndian, s.entsize)
	}
	out := body.Bytes()
	copy(out, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(out[16:], 3)    // ET_DYN
	binary.LittleEndian.PutUint16(out[18:], 0x3e) // EM_X86_64
	binary.LittleEndian.PutUint32(out[20:], 1)
	binary.LittleEndian.PutUint64(out[40:], shoff)
	binary.LittleEndian.PutUint16(out[52:], 64)
	binary.LittleEndian.PutUint16(out[58:], 64)
	binary.LittleEndian.PutUint16(out[60:], uint16(len(secs)))
	binary.LittleEndian.PutUint16(out[62:], shstrIdx)

	path := filepath.Join(t.TempDir(), "libds4.so")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func strp(s string) *string { return &s }

func fakeFatbin(ids ...string) []byte {
	var b bytes.Buffer
	b.WriteString("__CLANG_OFFLOAD_BUNDLE__")
	b.Write(make([]byte, 16))
	b.WriteString("host-x86_64-unknown-linux-gnu-")
	for _, id := range ids {
		b.Write(make([]byte, 24))
		b.WriteString("hipv4-amdgcn-amd-amdhsa--" + id)
	}
	b.Write(make([]byte, 32))
	return b.Bytes()
}

func TestLibraryArchsSymbol(t *testing.T) {
	path := writeELF(t, strp("gfx1151,gfx942"), fakeFatbin("gfx1100"))
	archs, src, isROCm, err := LibraryArchs(path)
	if err != nil || !isROCm || src != SourceSymbol || !slices.Equal(archs, []string{"gfx1151", "gfx942"}) {
		t.Fatalf("got %v %q %v %v", archs, src, isROCm, err)
	}
}

func TestLibraryArchsFatbinFallback(t *testing.T) {
	// An older library without the symbol: the uncompressed bundle IDs win.
	path := writeELF(t, nil, fakeFatbin("gfx1151", "gfx942:sramecc+:xnack-", "gfx11-generic", "gfx1151"))
	archs, src, isROCm, err := LibraryArchs(path)
	want := []string{"gfx1151", "gfx942:sramecc+:xnack-", "gfx11-generic"}
	if err != nil || !isROCm || src != SourceFatbin || !slices.Equal(archs, want) {
		t.Fatalf("got %v %q %v %v", archs, src, isROCm, err)
	}
}

func TestLibraryArchsUnknownAndNonROCm(t *testing.T) {
	// Compressed bundle: ROCm, but the list is unknown.
	path := writeELF(t, strp(""), []byte("CCOB\x02\x00compressed..."))
	archs, _, isROCm, err := LibraryArchs(path)
	if err != nil || !isROCm || len(archs) != 0 {
		t.Fatalf("compressed: got %v %v %v", archs, isROCm, err)
	}
	// A CUDA or CPU build has neither marker.
	path = writeELF(t, nil, nil)
	if _, _, isROCm, err := LibraryArchs(path); err != nil || isROCm {
		t.Fatalf("plain ELF: isROCm=%v err=%v", isROCm, err)
	}
	// Non-ELF (a Mach-O dylib, say) is not an error.
	other := filepath.Join(t.TempDir(), "libds4.dylib")
	os.WriteFile(other, []byte("\xcf\xfa\xed\xfe not elf"), 0o644)
	if _, _, isROCm, err := LibraryArchs(other); err != nil || isROCm {
		t.Fatalf("non-ELF: isROCm=%v err=%v", isROCm, err)
	}
}

func TestSupports(t *testing.T) {
	cases := []struct {
		lib, host string
		want      bool
	}{
		{"gfx942", "gfx942", true},
		{"gfx942:sramecc+:xnack-", "gfx942", true},
		{"gfx1151", "gfx942", false},
		{"gfx9-4-generic", "gfx942", true},
		{"gfx9-4-generic", "gfx950", true},
		{"gfx9-4-generic", "gfx90a", false},
		{"gfx11-generic", "gfx1151", true},
		{"gfx11-generic", "gfx1100", true},
		{"gfx11-generic", "gfx1201", false},
		{"gfx12-generic", "gfx1201", true},
		{"", "gfx942", false},
	}
	for _, c := range cases {
		if got := Supports(c.lib, c.host); got != c.want {
			t.Errorf("Supports(%q, %q) = %v, want %v", c.lib, c.host, got, c.want)
		}
	}
}

func TestParseKFDProperties(t *testing.T) {
	cases := map[string]string{
		"cpu_cores_count 0\ngfx_target_version 90402\nsimd_count 1216\n": "gfx942",
		"gfx_target_version 90010\n":                                     "gfx90a",
		"gfx_target_version 110501\n":                                    "gfx1151",
		"gfx_target_version 120001\n":                                    "gfx1201",
		"gfx_target_version 100300\n":                                    "gfx1030",
	}
	for in, want := range cases {
		if got, ok := ParseKFDProperties([]byte(in)); !ok || got != want {
			t.Errorf("ParseKFDProperties(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := ParseKFDProperties([]byte("cpu_cores_count 20\ngfx_target_version 0\n")); ok {
		t.Error("CPU node reported a GPU arch")
	}
}

func TestSysfsArchs(t *testing.T) {
	dir := t.TempDir()
	for node, props := range map[string]string{
		"0": "gfx_target_version 0\n",
		"1": "gfx_target_version 90402\n",
		"2": "gfx_target_version 110000\n",
		"3": "gfx_target_version 90402\n",
	} {
		os.MkdirAll(filepath.Join(dir, node), 0o755)
		os.WriteFile(filepath.Join(dir, node, "properties"), []byte(props), 0o644)
	}
	if got := sysfsArchs(dir); !slices.Equal(got, []string{"gfx942", "gfx1100"}) {
		t.Fatalf("sysfsArchs = %v", got)
	}
}

func TestParseRocminfo(t *testing.T) {
	out := `
*******
Agent 1
*******
  Name:                    AMD EPYC 9575F 64-Core Processor
  Marketing Name:          AMD EPYC 9575F 64-Core Processor
*******
Agent 2
*******
  Name:                    gfx942
  Marketing Name:          AMD Instinct MI325X
  ISA Info:
    ISA 1
      Name:                    amdgcn-amd-amdhsa--gfx942:sramecc+:xnack-
    ISA 2
      Name:                    amdgcn-amd-amdhsa--gfx9-4-generic:sramecc+:xnack-
`
	if got := ParseRocminfo([]byte(out)); !slices.Equal(got, []string{"gfx942"}) {
		t.Fatalf("ParseRocminfo = %v", got)
	}
}

func TestHostArchsOverride(t *testing.T) {
	t.Setenv("HSA_OVERRIDE_GFX_VERSION", "11.0.0")
	if got := HostArchs(context.Background()); !slices.Equal(got, []string{"gfx1100"}) {
		t.Fatalf("HostArchs with override = %v", got)
	}
}

func withHost(t *testing.T, archs ...string) {
	t.Helper()
	prev := hostArchsFunc
	hostArchsFunc = func(context.Context) []string { return archs }
	t.Cleanup(func() { hostArchsFunc = prev })
}

func TestCheck(t *testing.T) {
	t.Setenv(SkipEnv, "")
	strix := writeELF(t, strp("gfx1151"), nil)

	withHost(t, "gfx942")
	err := Check(context.Background(), strix, nil)
	var mm *MismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("Check = %v, want *MismatchError", err)
	}
	for _, want := range []string{"gfx1151", "gfx942", `ROCM_ARCHS="gfx942"`, SkipEnv} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}

	t.Setenv(SkipEnv, "1")
	if err := Check(context.Background(), strix, nil); err != nil {
		t.Fatalf("skip env: %v", err)
	}
	t.Setenv(SkipEnv, "")

	withHost(t, "gfx1151")
	if err := Check(context.Background(), strix, nil); err != nil {
		t.Fatalf("matching host: %v", err)
	}
	withHost(t) // no GPU detected: unknown, never blocks
	if err := Check(context.Background(), strix, nil); err != nil {
		t.Fatalf("unknown host: %v", err)
	}

	// Unknown library list falls back to install metadata.
	withHost(t, "gfx942")
	unknown := writeELF(t, nil, []byte("CCOB compressed"))
	if err := Check(context.Background(), unknown, nil); err != nil {
		t.Fatalf("unknown library: %v", err)
	}
	if err := Check(context.Background(), unknown, []string{"gfx1151"}); !errors.As(err, &mm) || mm.Source != SourceMetadata {
		t.Fatalf("metadata fallback: %v", err)
	}
	if err := Check(context.Background(), unknown, []string{"gfx9-4-generic"}); err != nil {
		t.Fatalf("metadata generic: %v", err)
	}

	// Non-ROCm libraries are never checked.
	if err := Check(context.Background(), writeELF(t, nil, nil), []string{"gfx1151"}); err != nil {
		t.Fatalf("non-ROCm: %v", err)
	}
}
