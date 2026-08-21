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
// Detecting upstream drift is therefore a manual sync step -- re-derive the
// layout from the current header and compare:
//
//	printf '#include <stddef.h>\n#include <stdio.h>\n#include "ds4.h"\n'\
//	  'int main(void){printf("%%zu\\n", sizeof(ds4_engine_options));}' > /tmp/z.c
//	cc -I/path/to/ds4 -o /tmp/z /tmp/z.c && /tmp/z   # must equal the size below
//
// When it differs, re-derive every offset with offsetof and update both the Go
// struct and these numbers -- never adjust the numbers to match the Go struct.

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
	checkOffsets(t, "ds4_engine_options", unsafe.Sizeof(o), 272, []fieldOffset{
		{"model_path", unsafe.Offsetof(o.ModelPath), 0},
		{"mtp_path", unsafe.Offsetof(o.MTPPath), 8},
		{"backend", unsafe.Offsetof(o.Backend), 16},
		{"n_threads", unsafe.Offsetof(o.NThreads), 20},
		{"context_size", unsafe.Offsetof(o.ContextSize), 24},
		{"prefill_chunk", unsafe.Offsetof(o.PrefillChunk), 28},
		{"mtp_draft_tokens", unsafe.Offsetof(o.MTPDraftTokens), 32},
		{"mtp_margin", unsafe.Offsetof(o.MTPMargin), 36},
		{"dspark_confidence_threshold", unsafe.Offsetof(o.DsparkConfidenceThreshold), 40},
		{"directional_steering_file", unsafe.Offsetof(o.DirectionalSteeringFile), 48},
		{"expert_profile_path", unsafe.Offsetof(o.ExpertProfilePath), 56},
		{"directional_steering_attn", unsafe.Offsetof(o.DirectionalSteeringAttn), 64},
		{"directional_steering_ffn", unsafe.Offsetof(o.DirectionalSteeringFFN), 68},
		{"power_percent", unsafe.Offsetof(o.PowerPercent), 72},
		{"ssd_streaming_cache_experts", unsafe.Offsetof(o.SSDStreamingCacheExperts), 76},
		{"ssd_streaming_cache_bytes", unsafe.Offsetof(o.SSDStreamingCacheBytes), 80},
		{"ssd_streaming_full_layers", unsafe.Offsetof(o.SSDStreamingFullLayers), 88},
		{"ssd_streaming_preload_experts", unsafe.Offsetof(o.SSDStreamingPreloadExperts), 92},
		{"simulate_used_memory_bytes", unsafe.Offsetof(o.SimulateUsedMemoryBytes), 96},
		{"warm_weights", unsafe.Offsetof(o.WarmWeights), 104},
		{"quality", unsafe.Offsetof(o.Quality), 105},
		{"glm_mtp", unsafe.Offsetof(o.GLMMTP), 106},
		{"glm_mtp_timing", unsafe.Offsetof(o.GLMMTPTiming), 107},
		{"dspark", unsafe.Offsetof(o.Dspark), 108},
		{"dspark_strict", unsafe.Offsetof(o.DsparkStrict), 109},
		{"dspark_confidence_threshold_set", unsafe.Offsetof(o.DsparkConfidenceThresholdSet), 110},
		{"cuda_tensor_parallel", unsafe.Offsetof(o.CUDATensorParallel), 111},
		{"ssd_streaming", unsafe.Offsetof(o.SSDStreaming), 112},
		{"ssd_streaming_cold", unsafe.Offsetof(o.SSDStreamingCold), 113},
		{"ssd_streaming_full_layers_set", unsafe.Offsetof(o.SSDStreamingFullLayersSet), 114},
		{"inspect_only", unsafe.Offsetof(o.InspectOnly), 115},
		{"placement_ctx_hint", unsafe.Offsetof(o.PlacementCtxHint), 116},
		{"placement_session_count_hint", unsafe.Offsetof(o.PlacementSessionCountHint), 120},
		{"share_session_prefill_workspace", unsafe.Offsetof(o.ShareSessionPrefillWorkspace), 124},
		{"first_token_test", unsafe.Offsetof(o.FirstTokenTest), 125},
		{"metal_graph_test", unsafe.Offsetof(o.MetalGraphTest), 126},
		{"load_slice", unsafe.Offsetof(o.LoadSlice), 127},
		{"load_layer_start", unsafe.Offsetof(o.LoadLayerStart), 128},
		{"load_layer_end", unsafe.Offsetof(o.LoadLayerEnd), 132},
		{"load_output", unsafe.Offsetof(o.LoadOutput), 136},
		{"distributed", unsafe.Offsetof(o.Distributed), 144},
		{"tp", unsafe.Offsetof(o.TP), 208},
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
