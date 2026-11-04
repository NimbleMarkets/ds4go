package openaiserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
)

// File identity deliberately errs toward cache misses. Model paths, sizes and
// modification times must match; native payload validation alone is insufficient
// to distinguish two weight files with the same tensor layout.
func cacheNamespace(cfg *cliopts.ServerConfig, opts ds4.EngineOptions) (string, error) {
	type identity struct {
		Path     string
		Size     int64
		Modified int64
	}
	var files []identity
	lib := ds4api.CurrentDefaultLibrary()
	if lib == nil {
		return "", fmt.Errorf("no loaded library")
	}
	for _, path := range []string{opts.ModelPath, opts.MTPPath, opts.VisionPath, cfg.DirSteeringFile, lib.Path()} {
		if path == "" {
			continue
		}
		path, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", err
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not a regular file", path)
		}
		files = append(files, identity{path, info.Size(), info.ModTime().UnixNano()})
	}
	// Runtime environment switches may change precision or checkpoint layout.
	// Include their values in the hash, never in the plaintext cache metadata.
	var environment []string
	for _, value := range os.Environ() {
		for _, prefix := range []string{"DS4_", "GGML_", "CUDA_", "HIP_", "HSA_", "ROCR_"} {
			if strings.HasPrefix(value, prefix) {
				environment = append(environment, value)
				break
			}
		}
	}
	slices.Sort(environment)
	data, err := json.Marshal(struct {
		Version     int
		Options     ds4.EngineOptions
		Files       []identity
		Environment []string
	}{1, opts, files, environment})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func coldCheckpointTokens(cfg *cliopts.ServerConfig, count int) int {
	if count < cfg.KVCacheMinTokens || count > cfg.KVCacheColdMaxTokens {
		return 0
	}
	n := count - cfg.KVCacheBoundaryTrimTokens
	if cfg.KVCacheBoundaryAlignTokens > 0 {
		n -= n % cfg.KVCacheBoundaryAlignTokens
	}
	if n < cfg.KVCacheMinTokens {
		return 0
	}
	return n
}

func (s *server) prepareCachedPrompt(ctx context.Context, session *ds4.Session, prompt *ds4.Prompt) (*ds4.Session, error) {
	if err := ctx.Err(); err != nil {
		return session, err
	}
	tokens := prompt.Tokens.Slice()
	var restored *ds4.Session
	hit, err := s.cache.restore(tokens, session.CommonPrefix(prompt.Tokens), func(path string, size uint64, expected []int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		session.Close()
		session = nil
		var err error
		restored, err = s.engine.NewSession(s.cfg.Ctx)
		if err != nil {
			return err
		}
		if err = restored.LoadPayloadFile(path, size); err != nil {
			return err
		}
		loaded := restored.Tokens()
		if loaded == nil {
			return fmt.Errorf("checkpoint restored no tokens")
		}
		ids := loaded.Slice()
		if !slices.Equal(ids, expected) {
			return fmt.Errorf("checkpoint token history mismatch")
		}
		return nil
	})
	if err != nil {
		if restored != nil {
			restored.Close()
			restored = nil
		}
		log.Printf("disk KV restore skipped: %v", err)
		if session == nil {
			session, err = s.engine.NewSession(s.cfg.Ctx)
			if err != nil {
				return nil, err
			}
		}
	} else if hit {
		session = restored
		log.Printf("disk KV hit: %d tokens", session.Tokens().Len())
	}
	if err := ctx.Err(); err != nil {
		return session, err
	}
	target := coldCheckpointTokens(s.cfg, len(tokens))
	// Never rewind a useful resident checkpoint just to write a shorter one.
	if target == 0 || session.CommonPrefix(prompt.Tokens) >= target {
		return session, nil
	}
	prefix, err := ds4api.NewTokens(tokens[:target])
	if err != nil {
		return session, err
	}
	defer prefix.Free()
	if err := s.syncPrompt(ctx, session, &ds4.Prompt{Tokens: prefix}); err != nil {
		return session, err
	}
	if err := ctx.Err(); err != nil {
		return session, err
	}
	size := session.PayloadBytes()
	if size == 0 || size > uint64(s.cache.budget) {
		log.Printf("disk KV store skipped: native payload bytes=%d, budget=%d", size, s.cache.budget)
		return session, nil
	}
	stored, err := s.cache.store(tokens[:target], size, session.SavePayloadFile)
	if err != nil {
		log.Printf("disk KV store skipped: %v", err)
	} else if stored {
		log.Printf("disk KV stored: %d tokens", target)
	}
	// The generation path syncs the full prompt, restoring valid logits even for
	// recurrent model families. Disk failures are cache misses, not request failures.
	return session, nil
}
