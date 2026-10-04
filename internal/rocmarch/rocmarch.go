// Package rocmarch reports which AMD GPU architectures a ROCm libds4 build
// supports and which architectures the host has, without cgo and without
// loading the library.
//
// A HIP fat binary only runs on the architectures it was compiled for, and a
// mismatch is not a load error: it surfaces as "invalid device function" on
// the first kernel launch. Checking up front lets ds4go fail with an
// actionable message instead.
package rocmarch

import (
	"bufio"
	"bytes"
	"context"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ArchSymbol is the exported libds4 data symbol holding the comma-separated
// --offload-arch list the library was built with (ds4 Makefile ROCM_ARCHS).
const ArchSymbol = "ds4_rocm_offload_archs"

// Source says where a library's architecture list came from.
type Source string

const (
	// SourceNone means the library is not a ROCm build, or its list is
	// unknown.
	SourceNone Source = ""
	// SourceSymbol is the ds4_rocm_offload_archs symbol in the library.
	SourceSymbol Source = "embedded symbol"
	// SourceFatbin is the uncompressed HIP offload bundle in .hip_fatbin.
	SourceFatbin Source = "HIP fat binary"
	// SourceMetadata is the rocm_archs field of ds4go-install.json.
	SourceMetadata Source = "install metadata"
)

// LibraryArchs reports the GPU architectures the ROCm library at path was
// compiled for.
//
// It reads the ELF file only; nothing is loaded or executed. isROCm is true
// when the file is recognizably a ROCm build (it has the arch symbol or a
// .hip_fatbin section). archs is empty when the list cannot be determined,
// for example an older library whose offload bundle is compressed; callers
// must treat that as unknown rather than unsupported.
func LibraryArchs(path string) (archs []string, src Source, isROCm bool, err error) {
	raw, err := os.Open(path)
	if err != nil {
		return nil, SourceNone, false, err
	}
	defer raw.Close()
	var magic [4]byte
	if _, err := io.ReadFull(raw, magic[:]); err != nil || string(magic[:]) != elf.ELFMAG {
		return nil, SourceNone, false, nil // not ELF (macOS, Windows)
	}
	f, err := elf.NewFile(raw)
	if err != nil {
		return nil, SourceNone, false, err
	}

	if list, ok := symbolString(f, ArchSymbol); ok {
		if archs := ParseList(list); len(archs) > 0 {
			return archs, SourceSymbol, true, nil
		}
		isROCm = true
	}
	if sec := f.Section(".hip_fatbin"); sec != nil {
		isROCm = true
		data, err := sec.Data()
		if err == nil {
			if archs := bundleArchs(data); len(archs) > 0 {
				return archs, SourceFatbin, true, nil
			}
		}
	}
	return nil, SourceNone, isROCm, nil
}

// symbolString returns the NUL-terminated string stored at the named dynamic
// (or static) symbol.
func symbolString(f *elf.File, name string) (string, bool) {
	syms, _ := f.DynamicSymbols()
	if s, ok := findSymbol(syms, name); ok {
		return readCString(f, s)
	}
	syms, _ = f.Symbols()
	if s, ok := findSymbol(syms, name); ok {
		return readCString(f, s)
	}
	return "", false
}

func findSymbol(syms []elf.Symbol, name string) (elf.Symbol, bool) {
	for _, s := range syms {
		if s.Name == name && s.Section != elf.SHN_UNDEF {
			return s, true
		}
	}
	return elf.Symbol{}, false
}

func readCString(f *elf.File, s elf.Symbol) (string, bool) {
	if int(s.Section) >= len(f.Sections) {
		return "", false
	}
	sec := f.Sections[s.Section]
	if sec.Type == elf.SHT_NOBITS || s.Value < sec.Addr {
		return "", false
	}
	off := s.Value - sec.Addr
	size := s.Size
	if size == 0 || size > 4096 {
		size = 4096
	}
	if off >= sec.Size {
		return "", false
	}
	if off+size > sec.Size {
		size = sec.Size - off
	}
	buf := make([]byte, size)
	if _, err := sec.ReadAt(buf, int64(off)); err != nil {
		return "", false
	}
	if i := bytes.IndexByte(buf, 0); i >= 0 {
		buf = buf[:i]
	}
	return string(buf), true
}

// bundleIDPattern matches clang offload bundle entry IDs such as
// "hipv4-amdgcn-amd-amdhsa--gfx942:sramecc+:xnack-" or
// "hipv4-amdgcn-amd-amdhsa--gfx11-generic". Compressed bundles ("CCOB")
// hide these IDs, so they yield no match.
var bundleIDPattern = regexp.MustCompile(`amdgcn-amd-amdhsa--(gfx[0-9a-z]+(?:-[0-9]+)?(?:-generic)?(?::[a-z]+[+-])*)`)

func bundleArchs(data []byte) []string {
	var out []string
	for _, m := range bundleIDPattern.FindAllSubmatch(data, -1) {
		out = append(out, string(m[1]))
	}
	return normalizeList(out)
}

// ParseList splits a space- or comma-separated architecture list, dropping
// empties and duplicates.
func ParseList(s string) []string {
	return normalizeList(strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}))
}

func normalizeList(in []string) []string {
	var out []string
	for _, a := range in {
		a = strings.TrimSpace(a)
		if a != "" && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

// BaseArch strips target-feature suffixes: "gfx942:sramecc+:xnack-" ->
// "gfx942".
func BaseArch(arch string) string {
	base, _, _ := strings.Cut(strings.TrimSpace(arch), ":")
	return strings.ToLower(base)
}

// genericFamilies maps each LLVM generic target to the processors whose code
// objects it can run on (LLVM AMDGPUUsage, "Generic Processors").
var genericFamilies = map[string][]string{
	"gfx9-generic":    {"gfx900", "gfx902", "gfx904", "gfx906", "gfx909", "gfx90c"},
	"gfx9-4-generic":  {"gfx940", "gfx941", "gfx942", "gfx950"},
	"gfx10-1-generic": {"gfx1010", "gfx1011", "gfx1012", "gfx1013"},
	"gfx10-3-generic": {"gfx1030", "gfx1031", "gfx1032", "gfx1033", "gfx1034", "gfx1035", "gfx1036"},
	"gfx11-generic":   {"gfx1100", "gfx1101", "gfx1102", "gfx1103", "gfx1150", "gfx1151", "gfx1152", "gfx1153"},
	"gfx12-generic":   {"gfx1200", "gfx1201"},
}

// Supports reports whether a library built for libArch can run on a host GPU
// whose processor is hostArch. Target features are ignored: a plain
// "gfx942" build runs on any sramecc/xnack mode, and builds that pin a
// feature are rare enough that the HIP runtime's own error is acceptable.
func Supports(libArch, hostArch string) bool {
	lib, host := BaseArch(libArch), BaseArch(hostArch)
	if lib == "" || host == "" {
		return false
	}
	if lib == host {
		return true
	}
	return slices.Contains(genericFamilies[lib], host)
}

// Compatible returns the host architectures that some library architecture
// supports.
func Compatible(libArchs, hostArchs []string) []string {
	var out []string
	for _, h := range hostArchs {
		for _, l := range libArchs {
			if Supports(l, h) {
				out = append(out, h)
				break
			}
		}
	}
	return out
}

// HostArchs reports the GPU processors present on this host, e.g.
// ["gfx942"], in device order with duplicates removed.
//
// HSA_OVERRIDE_GFX_VERSION, when set, wins: the ROCm runtime then loads
// code objects for that version regardless of the real hardware. Otherwise
// rocminfo is parsed when installed, falling back to the KFD topology in
// sysfs. An empty result means no GPU was found or detection failed, which
// callers must treat as unknown.
func HostArchs(ctx context.Context) []string {
	if v := os.Getenv("HSA_OVERRIDE_GFX_VERSION"); v != "" {
		if arch, ok := archFromDottedVersion(v); ok {
			return []string{arch}
		}
	}
	if archs := rocminfoArchs(ctx); len(archs) > 0 {
		return archs
	}
	return sysfsArchs(kfdTopologyDir)
}

// rocminfoTimeout bounds the rocminfo run; it initializes the HSA runtime and
// can stall on a wedged driver.
const rocminfoTimeout = 10 * time.Second

func rocminfoArchs(ctx context.Context) []string {
	bin := findRocminfo()
	if bin == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, rocminfoTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin).Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	return ParseRocminfo(out)
}

func findRocminfo() string {
	if p, err := exec.LookPath("rocminfo"); err == nil {
		return p
	}
	candidates := []string{"/opt/rocm/bin/rocminfo"}
	if matches, _ := filepath.Glob("/opt/rocm/core-*/bin/rocminfo"); len(matches) > 0 {
		candidates = append(candidates, matches...)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

var rocminfoAgentName = regexp.MustCompile(`^\s*Name:\s+(gfx[0-9a-f]+)\s*$`)

// ParseRocminfo extracts agent processor names ("Name: gfx942") from
// rocminfo output. ISA names ("amdgcn-amd-amdhsa--gfx942:...") are skipped;
// generic targets are matched through Supports instead.
func ParseRocminfo(out []byte) []string {
	var archs []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if m := rocminfoAgentName.FindStringSubmatch(sc.Text()); m != nil {
			archs = append(archs, m[1])
		}
	}
	return normalizeList(archs)
}

const kfdTopologyDir = "/sys/class/kfd/kfd/topology/nodes"

// sysfsArchs reads gfx_target_version from each KFD topology node. CPU nodes
// report 0 and are skipped.
func sysfsArchs(dir string) []string {
	nodes, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	// Numeric order keeps node 10 after node 9.
	slices.SortFunc(nodes, func(a, b os.DirEntry) int {
		ai, _ := strconv.Atoi(a.Name())
		bi, _ := strconv.Atoi(b.Name())
		return ai - bi
	})
	var archs []string
	for _, n := range nodes {
		data, err := os.ReadFile(filepath.Join(dir, n.Name(), "properties"))
		if err != nil {
			continue
		}
		if arch, ok := ParseKFDProperties(data); ok {
			archs = append(archs, arch)
		}
	}
	return normalizeList(archs)
}

// ParseKFDProperties returns the processor named by a KFD topology node's
// gfx_target_version (major*10000 + minor*100 + stepping, e.g. 90402 ->
// gfx942, 90010 -> gfx90a, 110501 -> gfx1151). ok is false for CPU nodes.
func ParseKFDProperties(data []byte) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 || fields[0] != "gfx_target_version" {
			continue
		}
		v, err := strconv.Atoi(fields[1])
		if err != nil || v <= 0 {
			return "", false
		}
		return archFromParts(v/10000, (v/100)%100, v%100)
	}
	return "", false
}

// archFromDottedVersion converts HSA_OVERRIDE_GFX_VERSION syntax ("11.0.0",
// "9.4.2", "10.3.0") to a processor name.
func archFromDottedVersion(v string) (string, bool) {
	parts := strings.Split(strings.TrimSpace(v), ".")
	if len(parts) != 3 {
		return "", false
	}
	var n [3]int
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil {
			return "", false
		}
		n[i] = x
	}
	return archFromParts(n[0], n[1], n[2])
}

// archFromParts formats gfx<major><minor><stepping>, with minor and stepping
// as single hex digits as LLVM names them.
func archFromParts(major, minor, stepping int) (string, bool) {
	if major <= 0 || minor < 0 || minor > 15 || stepping < 0 || stepping > 15 {
		return "", false
	}
	return fmt.Sprintf("gfx%d%x%x", major, minor, stepping), true
}

// MismatchError reports that no host GPU can run a ROCm library.
type MismatchError struct {
	Library      string
	LibraryArchs []string
	Source       Source
	HostArchs    []string
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("%s was built for AMD GPU architectures %s (%s), but this host has %s; "+
		"its kernels would fail with \"invalid device function\". "+
		"Rebuild libds4 for your GPU with: make shared-rocm ROCM_ARCHS=%q (in the ds4 checkout), "+
		"then install it with: ds4go install --pin ./libds4.so. "+
		"Set %s=1 to skip this check",
		e.Library, strings.Join(e.LibraryArchs, ","), e.Source,
		strings.Join(e.HostArchs, ","), strings.Join(baseArchs(e.HostArchs), " "),
		SkipEnv)
}

func baseArchs(archs []string) []string {
	var out []string
	for _, a := range archs {
		out = normalizeList(append(out, BaseArch(a)))
	}
	return out
}

// SkipEnv disables Check when set to a non-empty value other than "0".
const SkipEnv = "DS4_SKIP_GPU_ARCH_CHECK"

// Report is the outcome of comparing a library with the host.
type Report struct {
	ROCm         bool     // the library is a ROCm build
	LibraryArchs []string // empty when unknown
	Source       Source
	HostArchs    []string // empty when no GPU was detected
	Compatible   []string // host archs the library supports
}

// Known reports whether both sides of the comparison were determined.
func (r Report) Known() bool {
	return r.ROCm && len(r.LibraryArchs) > 0 && len(r.HostArchs) > 0
}

// OK reports whether the library can run on some host GPU, treating any
// unknown side as OK.
func (r Report) OK() bool {
	return !r.Known() || len(r.Compatible) > 0
}

// Inspect compares the library at libPath with the host GPUs. fallback is
// used as the library's list when the file itself does not say (typically
// the rocm_archs install-metadata field); pass nil when there is none.
// Host detection runs only for ROCm libraries.
func Inspect(ctx context.Context, libPath string, fallback []string) (Report, error) {
	archs, src, isROCm, err := LibraryArchs(libPath)
	if err != nil {
		return Report{}, err
	}
	if !isROCm {
		return Report{}, nil
	}
	if len(archs) == 0 && len(fallback) > 0 {
		archs, src = normalizeList(fallback), SourceMetadata
	}
	r := Report{ROCm: true, LibraryArchs: archs, Source: src}
	r.HostArchs = hostArchsFunc(ctx)
	r.Compatible = Compatible(r.LibraryArchs, r.HostArchs)
	return r, nil
}

var hostArchsFunc = HostArchs

// Err returns a *MismatchError for libPath when the report shows no host GPU
// the library supports, and nil otherwise. It does not consult SkipEnv.
func (r Report) Err(libPath string) error {
	if r.OK() {
		return nil
	}
	return &MismatchError{Library: libPath, LibraryArchs: r.LibraryArchs, Source: r.Source, HostArchs: r.HostArchs}
}

// Skipped reports whether SkipEnv disables the check.
func Skipped() bool {
	v := os.Getenv(SkipEnv)
	return v != "" && v != "0"
}

// Check returns a *MismatchError when libPath is a ROCm library and none of
// the detected host GPUs is in its architecture list. It returns nil when
// the library is not ROCm, either list is unknown, or SkipEnv is set.
func Check(ctx context.Context, libPath string, fallback []string) error {
	if Skipped() {
		return nil
	}
	r, err := Inspect(ctx, libPath, fallback)
	if err != nil {
		return nil // an unreadable file is the loader's problem to report
	}
	return r.Err(libPath)
}
