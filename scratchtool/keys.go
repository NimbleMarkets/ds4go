package scratchtool

import (
	"fmt"
	"strings"
)

// maxKeyLen bounds pad key and session names.
const maxKeyLen = 64

// indexFileName is reserved for a future list index and never names a pad key.
const indexFileName = "index.json"

// validateKey checks one pad key or session name: 1–64 characters from
// [a-z0-9._-], no leading '.' or '-', no "..", and not the reserved
// "index.json". Keys double as file names under the session directory, so
// these rules also keep every key a single safe path component.
func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("empty key")
	}
	if len(key) > maxKeyLen {
		return fmt.Errorf("key %q is %d characters; the limit is %d", key, len(key), maxKeyLen)
	}
	if key[0] == '.' || key[0] == '-' {
		return fmt.Errorf("key %q must not start with %q", key, string(key[0]))
	}
	if strings.Contains(key, "..") {
		return fmt.Errorf("key %q must not contain %q", key, "..")
	}
	if key == indexFileName {
		return fmt.Errorf("key %q is reserved", key)
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' {
			continue
		}
		return fmt.Errorf("key %q contains %q; keys use only a-z, 0-9, '.', '_', and '-'", key, string(key[i]))
	}
	return nil
}
