package openaiserver

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/NimbleMarkets/ds4go"
)

func requestSampling(req chatRequest) (ds4.GenerateOptions, []string, error) {
	opts := ds4.GenerateOptions{Temperature: ds4.DefaultTemperature, TopP: ds4.DefaultTopP, MinP: ds4.DefaultMinP, StopOnEOS: true}
	if req.Temperature != nil {
		opts.Temperature = *req.Temperature
	}
	if req.TopK != nil {
		opts.TopK = *req.TopK
	}
	if req.TopP != nil {
		opts.TopP = *req.TopP
	}
	if req.MinP != nil {
		opts.MinP = *req.MinP
	}
	if opts.Temperature < 0 || opts.TopK < 0 || int64(opts.TopK) > 1<<31-1 || opts.TopP < 0 || opts.TopP > 1 || opts.MinP < 0 || opts.MinP > 1 {
		return opts, nil, errors.New("temperature and top_k must be nonnegative; top_p and min_p must be in [0,1]")
	}
	if req.IgnoreEOS && (req.Temperature == nil || *req.Temperature != 0) {
		return opts, nil, errors.New("ignore_eos requires an explicit temperature of 0")
	}
	if req.Seed != nil {
		opts.Seed = *req.Seed
	} else {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return opts, nil, err
		}
		opts.Seed = binary.LittleEndian.Uint64(b[:])
	}
	var stops []string
	if len(req.Stop) > 0 && string(req.Stop) != "null" {
		var single string
		if json.Unmarshal(req.Stop, &single) == nil {
			stops = []string{single}
		} else if err := json.Unmarshal(req.Stop, &stops); err != nil {
			return opts, nil, errors.New("stop must be a string or an array of strings")
		}
	}
	for _, stop := range stops {
		if stop == "" {
			return opts, nil, errors.New("stop strings must not be empty")
		}
	}
	return opts, stops, nil
}

func selectedTools(req chatRequest) ([]chatTool, error) {
	if len(req.ToolChoice) == 0 || string(req.ToolChoice) == "null" {
		return req.Tools, nil
	}
	var choice string
	if err := json.Unmarshal(req.ToolChoice, &choice); err != nil {
		return nil, errors.New("only tool_choice auto and none are supported")
	}
	switch choice {
	case "auto":
		return req.Tools, nil
	case "none":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported tool_choice %q; use auto or none", choice)
	}
}

// stopFilter withholds only suffixes that could become a stop string, as well
// as incomplete UTF-8 runes. This prevents either from leaking into SSE JSON.
type stopFilter struct {
	stops   []string
	pending string
	stopped bool
	matched string
}

func (f *stopFilter) push(chunk string) string {
	if f.stopped {
		return ""
	}
	f.pending += chunk
	first := -1
	for _, stop := range f.stops {
		if at := strings.Index(f.pending, stop); at >= 0 && (first < 0 || at < first) {
			first = at
			f.matched = stop
		}
	}
	if first >= 0 {
		out := f.pending[:first]
		f.pending = ""
		f.stopped = true
		return out
	}
	keep := 0
	for _, stop := range f.stops {
		for n := 1; n < len(stop) && n <= len(f.pending); n++ {
			if n > keep && strings.HasSuffix(f.pending, stop[:n]) {
				keep = n
			}
		}
	}
	end := len(f.pending) - keep
	for end > 0 && !utf8.ValidString(f.pending[:end]) {
		_, size := utf8.DecodeLastRuneInString(f.pending[:end])
		if size > 1 {
			break
		}
		end--
	}
	out := f.pending[:end]
	f.pending = f.pending[end:]
	return out
}
func (f *stopFilter) close() string {
	if f.stopped {
		return ""
	}
	out := f.pending
	f.pending = ""
	return out
}
