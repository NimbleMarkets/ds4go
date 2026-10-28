package models

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureGGUF independently constructs shards or the expected merged file.
func fixtureGGUF(number int, names []string, alignment int, split bool) []byte {
	var b bytes.Buffer
	put := func(v any) {
		if err := binary.Write(&b, binary.LittleEndian, v); err != nil {
			panic(err)
		}
	}
	str := func(s string) { put(uint64(len(s))); b.WriteString(s) }
	nk := uint64(0)
	if number == 0 {
		nk = 2
	}
	if split {
		nk += 3
	}
	b.WriteString("GGUF")
	put(uint32(3))
	put(uint64(len(names)))
	put(nk)
	if number == 0 {
		str("general.architecture")
		put(uint32(8))
		str("deepseek41")
		str("general.alignment")
		put(uint32(4))
		put(uint32(alignment))
	}
	if split {
		str("split.no")
		put(uint32(2))
		put(uint16(number))
		str("split.count")
		put(uint32(2))
		put(uint16(2))
		str("split.tensors.count")
		put(uint32(5))
		put(int32(2))
	}
	for i, name := range names {
		str(name)
		put(uint32(1))
		put(uint64(4))
		put(uint32(0))
		put(uint64(i * alignment))
	}
	for b.Len()%alignment != 0 {
		b.WriteByte(0)
	}
	for _, name := range names {
		b.Write(bytes.Repeat([]byte{name[0]}, 16))
		b.Write(make([]byte, alignment-16))
	}
	return b.Bytes()
}
func mergeFixture(t *testing.T) ([]string, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for i, name := range []string{"a", "b"} {
		p := filepath.Join(dir, name+".gguf")
		if err := os.WriteFile(p, fixtureGGUF(i, []string{name}, 32, true), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths, filepath.Join(dir, "merged.gguf"), fixtureGGUF(0, []string{"a", "b"}, 16384, false)
}
func TestMergeGGUFResumeAndAlignment(t *testing.T) {
	for _, prefix := range []int{0, 11, 16389, 32768} {
		t.Run(string(rune('a'+prefix%26)), func(t *testing.T) {
			paths, out, want := mergeFixture(t)
			if err := os.WriteFile(out+".assembling", want[:prefix], 0600); err != nil {
				t.Fatal(err)
			}
			if err := mergeGGUF(context.Background(), paths, out, int64(len(want)), sha256Hex(string(want))); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(out)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("merged output differs: %v", err)
			}
			for _, p := range paths {
				if _, err := os.Stat(p); err != nil {
					t.Fatal("input removed", err)
				}
			}
		})
	}
}
func TestMergeGGUFFailuresPreserveFiles(t *testing.T) {
	for _, scenario := range []string{"order", "missing", "duplicate", "truncated", "checksum", "prefix", "size", "cancel", "existing"} {
		t.Run(scenario, func(t *testing.T) {
			paths, out, want := mergeFixture(t)
			sha := sha256Hex(string(want))
			size := int64(len(want))
			ctx := context.Background()
			switch scenario {
			case "order":
				paths[0], paths[1] = paths[1], paths[0]
			case "missing":
				paths = paths[:1]
			case "duplicate":
				os.WriteFile(paths[1], fixtureGGUF(1, []string{"a"}, 32, true), 0600)
			case "truncated":
				os.Truncate(paths[1], 30)
			case "checksum":
				sha = strings.Repeat("0", 64)
			case "prefix":
				os.WriteFile(out+".assembling", []byte("wrong"), 0600)
			case "size":
				size++
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "existing":
				os.WriteFile(out, []byte("keep me"), 0600)
			}
			if err := mergeGGUF(ctx, paths, out, size, sha); err == nil {
				t.Fatal("expected failure")
			}
			if scenario == "existing" {
				got, _ := os.ReadFile(out)
				if string(got) != "keep me" {
					t.Fatal("overwrote output")
				}
			} else if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("published failed merge")
			}
			if scenario == "prefix" {
				got, _ := os.ReadFile(out + ".assembling")
				if string(got) != "wrong" {
					t.Fatal("modified mismatched prefix")
				}
			}
			for _, p := range paths {
				if _, err := os.Stat(p); err != nil {
					t.Fatal("removed source")
				}
			}
		})
	}
}
func TestDownloadGGUFShards(t *testing.T) {
	m := testManager(t.TempDir())
	_, _, want := mergeFixture(t)
	model, _ := lookup("v41-mxfp4-1017")
	model.Parts = nil
	model.MergedBytes = int64(len(want))
	model.SHA256 = sha256Hex(string(want))
	payloads := map[string]string{}
	for i, name := range []string{"a", "b"} {
		data := fixtureGGUF(i, []string{name}, 32, true)
		file := name + ".gguf"
		payloads[file] = string(data)
		model.Parts = append(model.Parts, ModelPart{FileName: file, Bytes: int64(len(data)), SHA256: sha256Hex(string(data))})
	}
	for i := range curated {
		if curated[i].Alias == model.Alias {
			old := curated[i]
			curated[i] = model
			t.Cleanup(func() { curated[i] = old })
			break
		}
	}
	srv, _ := splitServer(t, payloads)
	withRepo(t, srv.URL)
	withAvailableBytes(t, func(string) (uint64, error) { return 1 << 60, nil })
	if _, err := m.Download(context.Background(), model.Alias, "", false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(m.ModelsDir, model.FileName))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("output: %v", err)
	}
	leftovers, err := m.Leftovers()
	if err != nil {
		t.Fatal(err)
	}
	shards := 0
	for _, l := range leftovers {
		if l.Kind == LeftoverPartial {
			if l.Alias != model.Alias {
				t.Fatal("missing shard owner")
			}
			shards++
		}
	}
	if shards != 2 {
		t.Fatalf("retained shards=%d", shards)
	}
	cfg, err := m.LoadConfig()
	if err != nil || cfg.DefaultModel != model.Alias {
		t.Fatalf("default not recorded: %v", err)
	}
}
func TestMXFP4Catalog(t *testing.T) {
	m, ok := Lookup("v41-mxfp4-1017")
	if !ok || !m.MergeGGUF || m.MergedBytes != 501609136128 || len(m.Parts) != 9 || !m.DeepSeek41 || m.Encoder != "v41-vision" {
		t.Fatalf("bad MXFP4 entry: %+v", m)
	}
	var total int64
	for _, p := range m.Parts {
		if len(p.SHA256) != 64 || p.Bytes <= 0 {
			t.Fatal("missing shard identity")
		}
		total += p.Bytes
	}
	if splitDownloadBytes(m) != total+m.MergedBytes {
		t.Fatal("disk budget omits shards or output")
	}
}

func TestGGUFSpaceAccountsForRetainedShards(t *testing.T) {
	m := testManager(t.TempDir())
	if err := os.MkdirAll(m.ModelsDir, 0755); err != nil {
		t.Fatal(err)
	}
	model := Model{FileName: "merged.gguf", MergeGGUF: true, MergedBytes: 100, Parts: []ModelPart{{FileName: "a.gguf", Bytes: 50}, {FileName: "b.gguf", Bytes: 60}}}
	for name, n := range map[string]int{"a.gguf": 50, "a.gguf.part": 50, "b.gguf.part": 20, "merged.gguf.assembling": 10} {
		if err := os.WriteFile(filepath.Join(m.ModelsDir, name), make([]byte, n), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.ggufDownloadedBytes(model); got != 80 {
		t.Fatalf("credited bytes = %d, want 80", got)
	}
	withAvailableBytes(t, func(string) (uint64, error) { return diskSpaceReserve + 129, nil })
	if err := m.checkCombinedDiskSpace([]Model{model}); err == nil {
		t.Fatal("combined preflight omitted retained shards")
	}
	if _, err := m.downloadGGUFShards(context.Background(), model, "", false); err == nil || !strings.Contains(err.Error(), "not enough free space") {
		t.Fatalf("expected disk preflight failure, got %v", err)
	}
}
