package ds4api

import (
	"testing"
	"unsafe"
)

// The C structs passed across the FFI boundary are read by libds4 at fixed
// byte offsets, so a layout mismatch corrupts every field from the drift point
// onward without any load-time error. These tests pin the Go mirrors to the
// layout of ds4.h as compiled for a 64-bit target (offsetof/sizeof ground truth
// taken from the upstream header; see docs/ROADMAP.md).
//
// These expectations are a recorded snapshot of the C layout, so they pin the
// Go mirrors against *that* snapshot. They cannot notice upstream moving: when
// ds4.h gains a field, both the Go struct and these numbers stay stale and the
// test still passes while the ABI silently breaks.
//
// Detecting upstream drift is therefore a separate step, which
// scripts/check-ds4-sync.sh performs: it recompiles the real header, reports
// every field named below through offsetof, and diffs that against these
// numbers. Run it after each upstream sync:
//
//	task ds4:sync           # or: DS4_SRC=../ds4 ./scripts/check-ds4-sync.sh
//
// When it reports drift, update both the Go struct in raw.go and these numbers
// to match the header -- never adjust the numbers to match the Go struct.
//
// The script parses the field names and offsets straight out of this file, so
// keep the {"c_field_name", unsafe.Offsetof(...), N} shape below intact.

type fieldOffset struct {
	name string
	got  uintptr
	want uintptr
}

func checkOffsets(t *testing.T, structName string, size, wantSize uintptr, fields []fieldOffset) {
	t.Helper()
	for _, f := range fields {
		if f.got != f.want {
			t.Errorf("%s.%s offset = %d, want %d", structName, f.name, f.got, f.want)
		}
	}
	if size != wantSize {
		t.Errorf("sizeof(%s) = %d, want %d", structName, size, wantSize)
	}
}

func TestEngineOptionsLayoutMatchesC(t *testing.T) {
	var o cEngineOptions
	checkOffsets(t, "ds4_engine_options", unsafe.Sizeof(o), 280, []fieldOffset{
		{"model_path", unsafe.Offsetof(o.ModelPath), 0},
		{"mtp_path", unsafe.Offsetof(o.MTPPath), 8},
		{"vision_path", unsafe.Offsetof(o.VisionPath), 16},
		{"backend", unsafe.Offsetof(o.Backend), 24},
		{"n_threads", unsafe.Offsetof(o.NThreads), 28},
		{"context_size", unsafe.Offsetof(o.ContextSize), 32},
		{"prefill_chunk", unsafe.Offsetof(o.PrefillChunk), 36},
		{"mtp_draft_tokens", unsafe.Offsetof(o.MTPDraftTokens), 40},
		{"mtp_margin", unsafe.Offsetof(o.MTPMargin), 44},
		{"dspark_confidence_threshold", unsafe.Offsetof(o.DsparkConfidenceThreshold), 48},
		{"directional_steering_file", unsafe.Offsetof(o.DirectionalSteeringFile), 56},
		{"expert_profile_path", unsafe.Offsetof(o.ExpertProfilePath), 64},
		{"directional_steering_attn", unsafe.Offsetof(o.DirectionalSteeringAttn), 72},
		{"directional_steering_ffn", unsafe.Offsetof(o.DirectionalSteeringFFN), 76},
		{"power_percent", unsafe.Offsetof(o.PowerPercent), 80},
		{"ssd_streaming_cache_experts", unsafe.Offsetof(o.SSDStreamingCacheExperts), 84},
		{"ssd_streaming_cache_bytes", unsafe.Offsetof(o.SSDStreamingCacheBytes), 88},
		{"ssd_streaming_full_layers", unsafe.Offsetof(o.SSDStreamingFullLayers), 96},
		{"ssd_streaming_preload_experts", unsafe.Offsetof(o.SSDStreamingPreloadExperts), 100},
		{"simulate_used_memory_bytes", unsafe.Offsetof(o.SimulateUsedMemoryBytes), 104},
		{"warm_weights", unsafe.Offsetof(o.WarmWeights), 112},
		{"quality", unsafe.Offsetof(o.Quality), 113},
		{"glm_mtp", unsafe.Offsetof(o.GLMMTP), 114},
		{"glm_mtp_timing", unsafe.Offsetof(o.GLMMTPTiming), 115},
		{"dspark", unsafe.Offsetof(o.Dspark), 116},
		{"dspark_strict", unsafe.Offsetof(o.DsparkStrict), 117},
		{"dspark_exact_sampling", unsafe.Offsetof(o.DsparkExactSampling), 118},
		{"dspark_confidence_threshold_set", unsafe.Offsetof(o.DsparkConfidenceThresholdSet), 119},
		{"cuda_tensor_parallel", unsafe.Offsetof(o.CUDATensorParallel), 120},
		{"ssd_streaming", unsafe.Offsetof(o.SSDStreaming), 121},
		{"ssd_streaming_cold", unsafe.Offsetof(o.SSDStreamingCold), 122},
		{"ssd_streaming_full_layers_set", unsafe.Offsetof(o.SSDStreamingFullLayersSet), 123},
		{"inspect_only", unsafe.Offsetof(o.InspectOnly), 124},
		{"placement_ctx_hint", unsafe.Offsetof(o.PlacementCtxHint), 128},
		{"placement_session_count_hint", unsafe.Offsetof(o.PlacementSessionCountHint), 132},
		{"share_session_prefill_workspace", unsafe.Offsetof(o.ShareSessionPrefillWorkspace), 136},
		{"first_token_test", unsafe.Offsetof(o.FirstTokenTest), 137},
		{"metal_graph_test", unsafe.Offsetof(o.MetalGraphTest), 138},
		{"load_slice", unsafe.Offsetof(o.LoadSlice), 139},
		{"load_layer_start", unsafe.Offsetof(o.LoadLayerStart), 140},
		{"load_layer_end", unsafe.Offsetof(o.LoadLayerEnd), 144},
		{"load_output", unsafe.Offsetof(o.LoadOutput), 148},
		{"distributed", unsafe.Offsetof(o.Distributed), 152},
		{"tp", unsafe.Offsetof(o.TP), 216},
	})
}

func TestTPOptionsLayoutMatchesC(t *testing.T) {
	var o cTPOptions
	checkOffsets(t, "ds4_tp_options", unsafe.Sizeof(o), 64, []fieldOffset{
		{"role", unsafe.Offsetof(o.Role), 0},
		{"requested", unsafe.Offsetof(o.Requested), 4},
		{"listen_host", unsafe.Offsetof(o.ListenHost), 8},
		{"listen_port", unsafe.Offsetof(o.ListenPort), 16},
		{"leader_host", unsafe.Offsetof(o.LeaderHost), 24},
		{"leader_port", unsafe.Offsetof(o.LeaderPort), 32},
		{"transport", unsafe.Offsetof(o.Transport), 36},
		{"rdma_device", unsafe.Offsetof(o.RDMADevice), 40},
		{"rdma_gid_index", unsafe.Offsetof(o.RDMAGIDIndex), 48},
		{"rdma_gid_index_set", unsafe.Offsetof(o.RDMAGIDIndexSet), 52},
		{"glm_token_prefill", unsafe.Offsetof(o.GLMTokenPrefill), 53},
		{"debug_hash", unsafe.Offsetof(o.DebugHash), 56},
	})
}

func TestDistributedOptionsLayoutMatchesC(t *testing.T) {
	var o cDistributedOptions
	checkOffsets(t, "ds4_distributed_options", unsafe.Sizeof(o), 64, []fieldOffset{
		{"role", unsafe.Offsetof(o.Role), 0},
		{"layers", unsafe.Offsetof(o.Layers), 4},
		{"listen_host", unsafe.Offsetof(o.ListenHost), 16},
		{"listen_port", unsafe.Offsetof(o.ListenPort), 24},
		{"coordinator_host", unsafe.Offsetof(o.CoordinatorHost), 32},
		{"coordinator_port", unsafe.Offsetof(o.CoordinatorPort), 40},
		{"prefill_chunk", unsafe.Offsetof(o.PrefillChunk), 44},
		{"prefill_window", unsafe.Offsetof(o.PrefillWindow), 48},
		{"activation_bits", unsafe.Offsetof(o.ActivationBits), 52},
		{"replay_check", unsafe.Offsetof(o.ReplayCheck), 56},
		{"debug", unsafe.Offsetof(o.Debug), 57},
	})

	var l cDistributedLayers
	if got := unsafe.Sizeof(l); got != 12 {
		t.Errorf("sizeof(ds4_distributed_layers) = %d, want 12", got)
	}
}

// ds4_vision_embedding and ds4_vision_span (upstream ds4 fc8bf3c). libds4
// fills these by offset, so a drifted layout corrupts token counts silently.
func TestVisionStructsLayoutMatchC(t *testing.T) {
	var e cVisionEmbedding
	checkOffsets(t, "ds4_vision_embedding", unsafe.Sizeof(e), 72, []fieldOffset{
		{"data", unsafe.Offsetof(e.Data), 0},
		{"token_count", unsafe.Offsetof(e.TokenCount), 8},
		{"layout", unsafe.Offsetof(e.Layout), 12},
		{"grid_width", unsafe.Offsetof(e.GridWidth), 16},
		{"grid_height", unsafe.Offsetof(e.GridHeight), 20},
		{"width", unsafe.Offsetof(e.Width), 24},
		{"height", unsafe.Offsetof(e.Height), 28},
		{"content_width", unsafe.Offsetof(e.ContentWidth), 32},
		{"content_height", unsafe.Offsetof(e.ContentHeight), 36},
		{"fingerprint", unsafe.Offsetof(e.Fingerprint), 40},
	})
	var s cVisionSpan
	checkOffsets(t, "ds4_vision_span", unsafe.Sizeof(s), 80, []fieldOffset{
		{"token_start", unsafe.Offsetof(s.TokenStart), 0},
		{"embedding", unsafe.Offsetof(s.Embedding), 8},
	})
}
