package cliopts

import (
	"github.com/spf13/pflag"
	"testing"
)

func TestServerCacheFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		bad  bool
	}{
		{nil, false},
		{[]string{"--kv-disk-dir=/tmp/cache"}, false},
		{[]string{"--kv-disk-space-mb=4096"}, true},
		{[]string{"--kv-cache-min-tokens=512"}, true},
		{[]string{"--kv-cache-reject-different-quant=true"}, true},
		{[]string{"--kv-disk-dir="}, true},
		{[]string{"--trace="}, true},
		{[]string{"--trace=x"}, true},
		{[]string{"--kv-disk-dir=x", "--kv-cache-continued-interval-tokens=10"}, true},
		{[]string{"--kv-disk-dir=x", "--kv-cache-reject-different-quant=false"}, true},
		{[]string{"--kv-disk-dir=x", "--kv-disk-space-mb=0"}, true},
		{[]string{"--kv-disk-dir=x", "--kv-cache-min-tokens=0"}, true},
		{[]string{"--kv-disk-dir=x", "--kv-cache-cold-max-tokens=0"}, false},
		{[]string{"--kv-disk-dir=x", "--kv-cache-boundary-trim-tokens=-1"}, true},
		{[]string{"--kv-disk-dir=x", "--kv-cache-continued-interval-tokens=0"}, false},
	} {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		cfg := RegisterServer(fs)
		if err := fs.Parse(tc.args); err != nil {
			t.Fatal(err)
		}
		if err := cfg.ValidateCacheFlags(); (err != nil) != tc.bad {
			t.Errorf("%v: %v, want bad=%v", tc.args, err, tc.bad)
		}
	}
}
