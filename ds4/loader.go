package ds4

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
)

// Library is a loaded libds4 shared library.
type Library struct {
	path   string
	handle uintptr
	raw    rawSymbols
}

var (
	defaultMu  sync.Mutex
	defaultLib *Library
)

// Load loads libds4 from path and registers all ds4.h symbols.
//
// Passing an empty path uses DS4_LIB, then searches DS4_DIR/lib and common
// local library locations.
func Load(path string) (*Library, error) {
	if path == "" {
		path = defaultLibraryPath()
	}
	if path == "" {
		return nil, fmt.Errorf("ds4: could not find %s; set DS4_LIB or DS4_DIR", libraryFileName())
	}

	handle, err := openDynamicLibrary(path)
	if err != nil {
		return nil, fmt.Errorf("ds4: load %q: %w", path, err)
	}

	lib := &Library{path: path, handle: handle}
	if err := lib.register(); err != nil {
		return nil, err
	}
	return lib, nil
}

// SetDefaultLibrary makes lib the package default library.
func SetDefaultLibrary(lib *Library) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultLib = lib
}

// DefaultLibrary returns the lazily loaded default library.
func DefaultLibrary() (*Library, error) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if defaultLib != nil {
		return defaultLib, nil
	}
	lib, err := Load("")
	if err != nil {
		return nil, err
	}
	defaultLib = lib
	return defaultLib, nil
}

// Path returns the filesystem path used to load this library.
func (l *Library) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// DefaultDir returns the ds4 data directory.
//
// DS4_DIR overrides the default. When DS4_DIR is unset, DefaultDir returns
// "$HOME/.ds4" when the user home directory can be determined, otherwise ".ds4".
func DefaultDir() string {
	if dir := os.Getenv("DS4_DIR"); dir != "" {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".ds4")
	}
	return ".ds4"
}

func (l *Library) register() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("ds4: register symbols from %q: %v", l.path, r)
		}
	}()
	r := &l.raw
	mustRegister := func(dst any, name string) {
		purego.RegisterLibFunc(dst, l.handle, name)
	}

	mustRegister(&r.ds4EngineOpen, "ds4_engine_open")
	mustRegister(&r.ds4EngineClose, "ds4_engine_close")
	mustRegister(&r.ds4EngineSummary, "ds4_engine_summary")
	mustRegister(&r.ds4BackendName, "ds4_backend_name")
	mustRegister(&r.ds4ThinkModeEnabled, "ds4_think_mode_enabled")
	mustRegister(&r.ds4ThinkModeName, "ds4_think_mode_name")
	mustRegister(&r.ds4ThinkMaxPrefix, "ds4_think_max_prefix")
	mustRegister(&r.ds4ThinkMaxMinContext, "ds4_think_max_min_context")
	mustRegister(&r.ds4ThinkModeForContext, "ds4_think_mode_for_context")
	mustRegister(&r.ds4ContextMemoryEstimate, "ds4_context_memory_estimate")
	mustRegister(&r.ds4LogIsTTY, "ds4_log_is_tty")
	mustRegister(&r.ds4LogString, "ds4_log")
	mustRegister(&r.ds4EngineGenerateArgmax, "ds4_engine_generate_argmax")
	mustRegister(&r.ds4EngineCollectIMatrix, "ds4_engine_collect_imatrix")
	mustRegister(&r.ds4EngineDumpTokens, "ds4_engine_dump_tokens")
	mustRegister(&r.ds4DumpTextTokenization, "ds4_dump_text_tokenization")
	mustRegister(&r.ds4EngineHeadTest, "ds4_engine_head_test")
	mustRegister(&r.ds4EngineFirstTokenTest, "ds4_engine_first_token_test")
	mustRegister(&r.ds4EngineMetalGraphTest, "ds4_engine_metal_graph_test")
	mustRegister(&r.ds4EngineMetalGraphFullTest, "ds4_engine_metal_graph_full_test")
	mustRegister(&r.ds4EngineMetalGraphPromptTest, "ds4_engine_metal_graph_prompt_test")
	mustRegister(&r.ds4TokensPush, "ds4_tokens_push")
	mustRegister(&r.ds4TokensFree, "ds4_tokens_free")
	mustRegister(&r.ds4TokensCopy, "ds4_tokens_copy")
	mustRegister(&r.ds4TokensStartsWith, "ds4_tokens_starts_with")
	mustRegister(&r.ds4TokenizeText, "ds4_tokenize_text")
	mustRegister(&r.ds4TokenizeRenderedChat, "ds4_tokenize_rendered_chat")
	mustRegister(&r.ds4ChatBegin, "ds4_chat_begin")
	mustRegister(&r.ds4EncodeChatPrompt, "ds4_encode_chat_prompt")
	mustRegister(&r.ds4ChatAppendMaxEffortPrefix, "ds4_chat_append_max_effort_prefix")
	mustRegister(&r.ds4ChatAppendMessage, "ds4_chat_append_message")
	mustRegister(&r.ds4ChatAppendAssistantPrefix, "ds4_chat_append_assistant_prefix")
	mustRegister(&r.ds4TokenText, "ds4_token_text")
	mustRegister(&r.ds4TokenEOS, "ds4_token_eos")
	mustRegister(&r.ds4TokenUser, "ds4_token_user")
	mustRegister(&r.ds4TokenAssistant, "ds4_token_assistant")
	mustRegister(&r.ds4SessionCreate, "ds4_session_create")
	mustRegister(&r.ds4SessionFree, "ds4_session_free")
	mustRegister(&r.ds4SessionSetProgress, "ds4_session_set_progress")
	mustRegister(&r.ds4SessionSync, "ds4_session_sync")
	mustRegister(&r.ds4SessionRewriteRequiresRebuild, "ds4_session_rewrite_requires_rebuild")
	mustRegister(&r.ds4SessionRewriteFromCommon, "ds4_session_rewrite_from_common")
	mustRegister(&r.ds4SessionCommonPrefix, "ds4_session_common_prefix")
	mustRegister(&r.ds4SessionArgmax, "ds4_session_argmax")
	mustRegister(&r.ds4SessionArgmaxExcluding, "ds4_session_argmax_excluding")
	mustRegister(&r.ds4SessionSample, "ds4_session_sample")
	mustRegister(&r.ds4SessionTopLogprobs, "ds4_session_top_logprobs")
	mustRegister(&r.ds4SessionTokenLogprob, "ds4_session_token_logprob")
	mustRegister(&r.ds4SessionEval, "ds4_session_eval")
	mustRegister(&r.ds4SessionEvalSpeculativeArgmax, "ds4_session_eval_speculative_argmax")
	mustRegister(&r.ds4SessionInvalidate, "ds4_session_invalidate")
	mustRegister(&r.ds4SessionRewind, "ds4_session_rewind")
	mustRegister(&r.ds4SessionPos, "ds4_session_pos")
	mustRegister(&r.ds4SessionCtx, "ds4_session_ctx")
	mustRegister(&r.ds4EngineRoutedQuantBits, "ds4_engine_routed_quant_bits")
	mustRegister(&r.ds4EngineHasMTP, "ds4_engine_has_mtp")
	mustRegister(&r.ds4EngineMTPDraftTokens, "ds4_engine_mtp_draft_tokens")
	mustRegister(&r.ds4SessionTokens, "ds4_session_tokens")
	mustRegister(&r.ds4SessionPayloadBytes, "ds4_session_payload_bytes")
	mustRegister(&r.ds4SessionSavePayload, "ds4_session_save_payload")
	mustRegister(&r.ds4SessionLoadPayload, "ds4_session_load_payload")
	mustRegister(&r.ds4SessionSaveSnapshot, "ds4_session_save_snapshot")
	mustRegister(&r.ds4SessionLoadSnapshot, "ds4_session_load_snapshot")
	mustRegister(&r.ds4SessionSnapshotFree, "ds4_session_snapshot_free")
	return nil
}

func defaultLibraryPath() string {
	if path := os.Getenv("DS4_LIB"); path != "" {
		return path
	}

	name := libraryFileName()
	var candidates []string
	ds4Dir := DefaultDir()
	if ds4Dir != "" {
		candidates = append(candidates, filepath.Join(ds4Dir, "lib", name))
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, name), filepath.Join(dir, "lib", name))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, name), filepath.Join(cwd, "lib", name))
	}
	candidates = append(candidates, name)

	for _, candidate := range candidates {
		if candidate == name {
			return candidate
		}
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return ""
}

func libraryFileName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libds4.dylib"
	case "windows":
		return "libds4.dll"
	default:
		return "libds4.so"
	}
}

func ensureLibrary(lib *Library) (*Library, error) {
	if lib != nil {
		return lib, nil
	}
	return DefaultLibrary()
}

var errClosed = errors.New("ds4: object is closed")
