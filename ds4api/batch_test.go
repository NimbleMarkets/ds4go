package ds4api

import (
	"testing"
	"unsafe"
)

func TestEvalBatchNativeAndFallback(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "fallback", true: "native"}[native], func(t *testing.T) {
			lib := NewMockLibrary()
			engine, err := lib.NewEngine(EngineOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			items := make([]DecodeItem, 2)
			for i := range items {
				s, err := engine.NewSession(64)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				if err := s.Sync([]int{1, 2, 3}); err != nil {
					t.Fatal(err)
				}
				items[i] = DecodeItem{Session: s, Token: 10 + i}
			}
			calls := 0
			if native {
				lib.raw.ds4SessionsEvalBatch = func(ptr unsafe.Pointer, n int32, errbuf unsafe.Pointer, errlen uintptr) int32 {
					calls++
					if n != 2 {
						t.Errorf("batch count=%d", n)
					}
					raw := unsafe.Slice((*cDecodeItem)(ptr), int(n))
					for i, item := range raw {
						if item.Session != items[i].Session.ptr || item.Token != int32(items[i].Token) {
							t.Error("wrong ABI item")
						}
						if code := lib.raw.ds4SessionEval(item.Session, item.Token, errbuf, errlen); code != 0 {
							return code
						}
					}
					return 0
				}
			}
			if err := engine.EvalBatch(items); err != nil {
				t.Fatal(err)
			}
			for _, item := range items {
				if item.Session.Pos() != 4 || item.Session.Tokens().Slice()[3] != item.Token {
					t.Error("session was not advanced")
				}
			}
			if native && calls != 1 {
				t.Errorf("native calls=%d", calls)
			}
			if err := engine.EvalBatch(nil); err != nil {
				t.Fatal(err)
			}
			if err := engine.EvalBatch([]DecodeItem{items[0], items[0]}); err == nil {
				t.Error("duplicate accepted")
			}
			if items[0].Session.Pos() != 4 {
				t.Error("invalid batch advanced a session")
			}
			other, err := lib.NewEngine(EngineOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			if err := other.EvalBatch(items); err == nil {
				t.Error("foreign sessions accepted")
			}
			items[0].Session.Close()
			if err := engine.EvalBatch(items); err == nil {
				t.Error("closed session accepted")
			}
		})
	}
}
