package models

// This merger follows ds4's gguf-tools/merge_deepseek41_shards.py. Standard
// GGUF shards have independent headers; concatenating them is not a merge.
import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const ggufAlignment uint64 = 16384
const ggufHeaderLimit = 128 << 20

func ggufAlign(n, a uint64) uint64 { return (n + a - 1) / a * a }

type ggufKV struct {
	key    string
	kind   uint32
	raw    []byte
	number uint64
	text   string
}
type ggufTensor struct {
	name         string
	shape        []uint64
	kind         uint32
	offset, size uint64
}
type ggufHeader struct {
	kv      []ggufKV
	tensors []ggufTensor
	data    uint64
}
type ggufReader struct {
	r   io.ReadSeeker
	pos uint64
	err error
}

func (r *ggufReader) read(n uint64) []byte {
	if r.err != nil {
		return nil
	}
	if n > ggufHeaderLimit || r.pos+n > ggufHeaderLimit {
		r.err = fmt.Errorf("GGUF header exceeds limit")
		return nil
	}
	b := make([]byte, int(n))
	_, r.err = io.ReadFull(r.r, b)
	r.pos += n
	return b
}
func (r *ggufReader) u32() uint32 {
	b := r.read(4)
	if len(b) != 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}
func (r *ggufReader) u64() uint64 {
	b := r.read(8)
	if len(b) != 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}
func (r *ggufReader) str() string { return string(r.read(r.u64())) }
func (r *ggufReader) value(k uint32, depth int) {
	if r.err != nil {
		return
	}
	if depth > 4 {
		r.err = fmt.Errorf("nested GGUF metadata exceeds limit")
		return
	}
	switch k {
	case 0, 1, 7:
		r.read(1)
	case 2, 3:
		r.read(2)
	case 4, 5, 6:
		r.read(4)
	case 10, 11, 12:
		r.read(8)
	case 8:
		r.str()
	case 9:
		kind, n := r.u32(), r.u64()
		if n > ggufHeaderLimit {
			r.err = fmt.Errorf("invalid GGUF array length")
			return
		}
		for i := uint64(0); i < n && r.err == nil; i++ {
			r.value(kind, depth+1)
		}
	default:
		r.err = fmt.Errorf("unsupported GGUF metadata type %d", k)
	}
}
func readGGUF(f *os.File) (ggufHeader, error) {
	var h ggufHeader
	fail := func() (ggufHeader, error) { return h, fmt.Errorf("invalid MXFP4 GGUF shard %s", f.Name()) }
	st, err := f.Stat()
	if err != nil {
		return h, err
	}
	r := ggufReader{r: f}
	if string(r.read(4)) != "GGUF" || r.u32() != 3 {
		return fail()
	}
	nt, nk := r.u64(), r.u64()
	if nt > 100000 || nk > 100000 {
		return fail()
	}
	seen := map[string]bool{}
	alignment := uint64(32)
	for i := uint64(0); i < nk; i++ {
		start := r.pos
		key, k := r.str(), r.u32()
		if seen[key] {
			return fail()
		}
		seen[key] = true
		vstart := r.pos
		r.value(k, 0)
		end := r.pos
		if r.err != nil {
			return fail()
		}
		raw := make([]byte, end-start)
		if _, err := f.ReadAt(raw, int64(start)); err != nil {
			return h, err
		}
		v := ggufKV{key: key, kind: k, raw: raw}
		data := raw[vstart-start:]
		switch k {
		case 2:
			v.number = uint64(binary.LittleEndian.Uint16(data))
		case 4, 5:
			v.number = uint64(binary.LittleEndian.Uint32(data))
		case 8:
			v.text = string(data[8:])
		}
		if key == "general.alignment" {
			if k != 4 {
				return fail()
			}
			alignment = v.number
		}
		h.kv = append(h.kv, v)
	}
	if alignment == 0 || alignment > ggufAlignment || alignment&(alignment-1) != 0 {
		return fail()
	}
	offset := uint64(0)
	for i := uint64(0); i < nt; i++ {
		t := ggufTensor{name: r.str()}
		rank := r.u32()
		if rank < 1 || rank > 4 {
			return fail()
		}
		count := uint64(1)
		for j := uint32(0); j < rank; j++ {
			n := r.u64()
			if n == 0 || n > (1<<60)/count {
				return fail()
			}
			count *= n
			t.shape = append(t.shape, n)
		}
		t.kind = r.u32()
		t.offset = r.u64()
		block, size := uint64(1), uint64(0)
		switch t.kind {
		case 0:
			size = 4
		case 1:
			size = 2
		case 8:
			block, size = 32, 34
		case 24:
			size = 1
		case 39:
			block, size = 32, 17
		default:
			return fail()
		}
		if t.shape[0]%block != 0 || t.offset != offset {
			return fail()
		}
		t.size = count / block * size
		if t.size > uint64(st.Size()) || offset > uint64(st.Size())-t.size {
			return fail()
		}
		offset += ggufAlign(t.size, alignment)
		h.tensors = append(h.tensors, t)
	}
	if r.err != nil {
		return fail()
	}
	h.data = ggufAlign(r.pos, alignment)
	if h.data+offset != uint64(st.Size()) {
		return fail()
	}
	return h, nil
}
func ggufPut(b *bytes.Buffer, v any)       { _ = binary.Write(b, binary.LittleEndian, v) }
func ggufString(b *bytes.Buffer, s string) { ggufPut(b, uint64(len(s))); b.WriteString(s) }
func ggufPlan(headers []ggufHeader) ([]byte, uint64, error) {
	bad := func() ([]byte, uint64, error) { return nil, 0, fmt.Errorf("invalid GGUF shard set or metadata") }
	if len(headers) == 0 {
		return bad()
	}
	names := map[string]bool{}
	var tensors []ggufTensor
	total := 0
	for _, h := range headers {
		total += len(h.tensors)
	}
	var metadata [][]byte
	for i, h := range headers {
		split := map[string]bool{}
		arch, alignment := false, false
		for _, v := range h.kv {
			switch v.key {
			case "split.no":
				if v.kind != 2 || v.number != uint64(i) {
					return bad()
				}
				split[v.key] = true
			case "split.count":
				if v.kind != 2 || v.number != uint64(len(headers)) {
					return bad()
				}
				split[v.key] = true
			case "split.tensors.count":
				if v.kind != 5 || v.number != uint64(total) {
					return bad()
				}
				split[v.key] = true
			default:
				if i != 0 || strings.HasPrefix(v.key, "split.") {
					return bad()
				}
				raw := v.raw
				if v.key == "general.architecture" {
					arch = v.kind == 8 && v.text == "deepseek41"
				}
				if v.key == "general.alignment" {
					alignment = v.kind == 4
					var b bytes.Buffer
					ggufString(&b, v.key)
					ggufPut(&b, uint32(4))
					ggufPut(&b, uint32(ggufAlignment))
					raw = b.Bytes()
				}
				metadata = append(metadata, raw)
			}
		}
		if len(split) != 3 || (i == 0 && (!arch || !alignment)) {
			return bad()
		}
		for _, t := range h.tensors {
			if names[t.name] {
				return bad()
			}
			names[t.name] = true
			tensors = append(tensors, t)
		}
	}
	var b bytes.Buffer
	b.WriteString("GGUF")
	ggufPut(&b, uint32(3))
	ggufPut(&b, uint64(total))
	ggufPut(&b, uint64(len(metadata)))
	for _, raw := range metadata {
		b.Write(raw)
	}
	offset := uint64(0)
	for _, t := range tensors {
		ggufString(&b, t.name)
		ggufPut(&b, uint32(len(t.shape)))
		for _, n := range t.shape {
			ggufPut(&b, n)
		}
		ggufPut(&b, t.kind)
		ggufPut(&b, offset)
		offset += ggufAlign(t.size, ggufAlignment)
	}
	b.Write(make([]byte, ggufAlign(uint64(b.Len()), ggufAlignment)-uint64(b.Len())))
	return b.Bytes(), uint64(b.Len()) + offset, nil
}

// mergeGGUF verifies an interrupted prefix against the source, then resumes.
// Inputs are retained and the final file is published only after verification.
func mergeGGUF(ctx context.Context, paths []string, out string, expected int64, sha string) error {
	var files []*os.File
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	var headers []ggufHeader
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		files = append(files, f)
		h, err := readGGUF(f)
		if err != nil {
			return err
		}
		headers = append(headers, h)
	}
	header, total, err := ggufPlan(headers)
	if err != nil {
		return err
	}
	if total != uint64(expected) {
		return fmt.Errorf("merged GGUF size %d does not match expected %d", total, expected)
	}
	pending := out + ".assembling"
	dst, err := os.OpenFile(pending, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer dst.Close()
	st, err := dst.Stat()
	if err != nil {
		return err
	}
	completed := st.Size()
	if completed > expected {
		return fmt.Errorf("partial assembly exceeds expected size")
	}
	pos := int64(0)
	appendData := func(data []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		overlap := min(int64(len(data)), max(int64(0), completed-pos))
		if overlap > 0 {
			old := make([]byte, overlap)
			if _, err := io.ReadFull(dst, old); err != nil {
				return err
			}
			if !bytes.Equal(old, data[:overlap]) {
				return fmt.Errorf("partial assembly differs from shards; move it aside before retrying")
			}
		}
		if _, err := dst.Write(data[overlap:]); err != nil {
			return err
		}
		pos += int64(len(data))
		return nil
	}
	if err := appendData(header); err != nil {
		return err
	}
	buf := make([]byte, 16<<20)
	for i, h := range headers {
		for _, t := range h.tensors {
			if _, err := files[i].Seek(int64(h.data+t.offset), io.SeekStart); err != nil {
				return err
			}
			for left := t.size; left > 0; {
				n := min(left, uint64(len(buf)))
				if _, err := io.ReadFull(files[i], buf[:n]); err != nil {
					return err
				}
				if err := appendData(buf[:n]); err != nil {
					return err
				}
				left -= n
			}
			if err := appendData(make([]byte, ggufAlign(t.size, ggufAlignment)-t.size)); err != nil {
				return err
			}
		}
	}
	if err := dst.Sync(); err != nil {
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	if err := validateSHA256(pending, sha); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Link(pending, out); err != nil {
		return err
	}
	return os.Remove(pending)
}

func (m *Manager) downloadGGUFShards(ctx context.Context, model Model, token string, force bool) (Model, error) {
	out := filepath.Join(m.ModelsDir, model.FileName)
	lock, err := TryLock(out + ".lock")
	if err != nil {
		return Model{}, err
	}
	defer lock.Close()
	if _, err := os.Stat(out); err == nil {
		if err := validateSHA256(out, model.SHA256); err != nil {
			return Model{}, err
		}
		return model, nil
	}
	if !force {
		have := m.ggufDownloadedBytes(model)
		if err := m.checkDiskSpace(m.ModelsDir, model.FileName, splitDownloadBytes(model), have); err != nil {
			return Model{}, err
		}
	}
	var paths []string
	for _, part := range model.Parts {
		path := filepath.Join(m.ModelsDir, part.FileName)
		if err := m.ensurePart(ctx, model, part, path, token); err != nil {
			return Model{}, err
		}
		st, err := os.Stat(path)
		if err != nil {
			return Model{}, err
		}
		if st.Size() != part.Bytes {
			return Model{}, fmt.Errorf("incorrect shard size: %s", path)
		}
		paths = append(paths, path)
	}
	fmt.Fprintln(m.Out, "Merging GGUF shards with 16 KiB alignment; retaining input shards")
	if err := mergeGGUF(ctx, paths, out, model.MergedBytes, model.SHA256); err != nil {
		return Model{}, err
	}
	stateLock, err := m.lockState()
	if err != nil {
		return Model{}, err
	}
	defer stateLock.Close()
	return m.recordDownloadLocked(model, model.SHA256)
}

// ggufDownloadedBytes caps each artifact's contribution so stale or duplicate
// resume files cannot make the disk-space preflight underestimate the budget.
func (m *Manager) ggufDownloadedBytes(model Model) int64 {
	size := func(name string, limit int64) int64 {
		st, err := os.Stat(filepath.Join(m.ModelsDir, name))
		if err != nil || !st.Mode().IsRegular() {
			return 0
		}
		return min(st.Size(), limit)
	}
	have := size(model.FileName+".assembling", model.MergedBytes)
	for _, part := range model.Parts {
		have += max(size(part.FileName, part.Bytes), size(part.FileName+".part", part.Bytes))
	}
	return have
}
