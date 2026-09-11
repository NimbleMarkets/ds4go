package ds4

import (
	"container/list"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/internal/models"
)

// ImageInput is one image for a multimodal message: encoded PNG or JPEG
// bytes, or a path to such a file. Exactly one of Data or Path is set.
type ImageInput struct {
	Data []byte
	Path string
}

// ContentPart is one segment of a multimodal message: text or an image.
type ContentPart struct {
	Text  string
	Image *ImageInput
}

// Prompt is a rendered prompt: its tokens plus the image spans libds4
// produced while rendering. Sync it with Generator.GeneratePrompt (or
// Session.SyncMultimodal). Free releases both; libds4 keeps only image
// identities after a sync, so a Prompt may be freed once it has been synced.
type Prompt struct {
	Tokens *Tokens
	Images []ds4api.VisionSpan
}

// Free releases the tokens and every image embedding. It is idempotent.
func (p *Prompt) Free() {
	if p == nil {
		return
	}
	ds4api.FreeVisionSpans(p.Images)
	p.Images = nil
	if p.Tokens != nil {
		p.Tokens.Free()
		p.Tokens = nil
	}
}

// Cache defaults, matching upstream ds4-server's image cache.
const (
	DefaultImageCacheEntries = 32
	DefaultImageCacheBytes   = int64(128 << 20)
)

// ImageEncoder encodes images through an engine's vision encoder and keeps
// an LRU cache of embeddings keyed by the image bytes. Encoding is a GPU
// pass, and the tool loop re-renders the prompt every round, so repeated
// images must not be re-encoded. Every Encode returns an independent clone;
// the caller frees it (or the Prompt that absorbs it).
type ImageEncoder struct {
	engine *Engine

	mu         sync.Mutex
	maxEntries int
	maxBytes   int64
	bytes      int64
	order      *list.List // front = most recently used
	entries    map[[32]byte]*list.Element
}

type imageCacheEntry struct {
	key   [32]byte
	emb   *ds4api.VisionEmbedding
	bytes int64
}

// NewImageEncoder returns an encoder over engine with the default limits.
func NewImageEncoder(engine *Engine) *ImageEncoder {
	return &ImageEncoder{
		engine:     engine,
		maxEntries: DefaultImageCacheEntries,
		maxBytes:   DefaultImageCacheBytes,
		order:      list.New(),
		entries:    map[[32]byte]*list.Element{},
	}
}

// SetLimits changes the cache bounds and evicts down to them.
func (c *ImageEncoder) SetLimits(entries int, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxEntries, c.maxBytes = entries, bytes
	// Nothing is arriving, so the cache may stay at exactly the new limits.
	c.evictLocked(0, 0)
}

// Stats reports the cache occupancy.
func (c *ImageEncoder) Stats() (entries int, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.bytes
}

func (c *ImageEncoder) cached(data []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.entries[sha256.Sum256(data)]
	return ok
}

// Encode returns an embedding for img, from the cache when the same bytes
// were encoded before. The result is a clone the caller owns.
func (c *ImageEncoder) Encode(img ImageInput) (*ds4api.VisionEmbedding, error) {
	data, err := img.bytes()
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256(data)

	c.mu.Lock()
	// Snapshot the byte budget under the lock: SetLimits writes it, and the
	// cacheability check below runs outside the critical section.
	maxBytes := c.maxBytes
	if el, ok := c.entries[key]; ok {
		c.order.MoveToFront(el)
		emb := el.Value.(*imageCacheEntry).emb
		// Clone while still holding c.mu: if we unlocked first, a
		// concurrent Encode could evict this entry and Free its embedding
		// before Clone runs, cloning already-freed memory. Lock order is
		// c.mu -> libCallMu (Clone takes libCallMu internally), which
		// matches evictLocked's Free calls, so this cannot deadlock.
		clone, err := emb.Clone()
		c.mu.Unlock()
		return clone, err
	}
	c.mu.Unlock()

	emb, err := c.engine.VisionEncodeMemory(data)
	if err != nil {
		return nil, err
	}
	size := int64(emb.TokenCount()) * int64(c.engine.EmbdDim()) * 4
	if size <= 0 || size > maxBytes {
		// Not cacheable; hand the fresh embedding straight to the caller.
		return emb, nil
	}
	clone, err := emb.Clone()
	if err != nil {
		emb.Free()
		return nil, err
	}
	c.mu.Lock()
	if _, ok := c.entries[key]; ok {
		// Raced with another encode of the same image; keep the first.
		c.mu.Unlock()
		emb.Free()
		return clone, nil
	}
	if c.maxEntries <= 0 || size > c.maxBytes {
		// The cache may hold nothing (a zero entry limit), or SetLimits
		// shrank the budget while this image was encoding; caching it now
		// would overrun the limit however much we evicted.
		c.mu.Unlock()
		emb.Free()
		return clone, nil
	}
	c.evictLocked(1, size)
	el := c.order.PushFront(&imageCacheEntry{key: key, emb: emb, bytes: size})
	c.entries[key] = el
	c.bytes += size
	c.mu.Unlock()
	return clone, nil
}

// evictLocked frees least-recently-used entries until the cache has room for
// incomingEntries more entries holding incomingBytes more bytes within both
// limits. Insertion passes (1, size); a resize passes (0, 0), which trims to
// the new limits without evicting an entry the cache is still allowed to
// hold. Called with mu held.
func (c *ImageEncoder) evictLocked(incomingEntries int, incomingBytes int64) {
	for c.order.Len() > 0 && (c.order.Len()+incomingEntries > c.maxEntries || c.bytes+incomingBytes > c.maxBytes) {
		el := c.order.Back()
		entry := el.Value.(*imageCacheEntry)
		c.order.Remove(el)
		delete(c.entries, entry.key)
		c.bytes -= entry.bytes
		entry.emb.Free()
	}
}

// DefaultVisionPath returns the installed encoder for a catalog-recognized
// vision model, or "" when the model is text-only or its encoder is absent.
func DefaultVisionPath(modelPath string) string {
	model, ok := models.ModelForPath(modelPath)
	if !ok || !model.Vision || model.Encoder == "" {
		return ""
	}
	encoder, ok := models.Lookup(model.Encoder)
	if !ok {
		return ""
	}
	path := filepath.Join(models.NewManager().ModelsDir, encoder.FileName)
	if st, err := os.Stat(path); err != nil || st.IsDir() || st.Size() == 0 {
		return ""
	}
	return path
}

// DSparkSupportPath reports the DSpark drafter a catalog-recognized model
// pins. required is true when the catalog entry names its own DSpark support
// model (the Vision-Exp checkpoints do); libds4 rejects every other drafter
// for such a model, including the standard 0731 one. path is that drafter's
// installed file under the models dir, or "" when it is not installed.
// Text-only, GLM, and unrecognized models return ("", false).
func DSparkSupportPath(modelPath string) (path string, required bool) {
	model, ok := models.ModelForPath(modelPath)
	if !ok || model.GLM || model.DSpark == "" {
		return "", false
	}
	drafter, ok := models.Lookup(model.DSpark)
	if !ok {
		return "", true
	}
	path = filepath.Join(models.NewManager().ModelsDir, drafter.FileName)
	if st, err := os.Stat(path); err != nil || st.IsDir() || st.Size() == 0 {
		return "", true
	}
	return path, true
}

// ApplyVisionDefaults fills EngineOptions.VisionPath with the installed
// encoder paired to a catalog vision model. An explicit VisionPath wins.
func ApplyVisionDefaults(opts *EngineOptions) {
	if opts == nil || opts.VisionPath != "" {
		return
	}
	opts.VisionPath = DefaultVisionPath(opts.ModelPath)
}

func (img ImageInput) bytes() ([]byte, error) {
	switch {
	case len(img.Data) > 0 && img.Path != "":
		return nil, errors.New("ds4go: ImageInput has both Data and Path")
	case len(img.Data) > 0:
		return img.Data, nil
	case img.Path != "":
		data, err := os.ReadFile(img.Path)
		if err != nil {
			return nil, fmt.Errorf("ds4go: read image: %w", err)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("ds4go: image %s is empty", img.Path)
		}
		return data, nil
	default:
		return nil, errors.New("ds4go: ImageInput has neither Data nor Path")
	}
}
