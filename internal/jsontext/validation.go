package jsontext

import (
	"encoding/json"
	"strconv"
	"unicode/utf8"
)

// Lossless reports whether encoding/json can decode raw without repairing
// malformed UTF-8 or unpaired UTF-16 surrogate escapes to U+FFFD.
func Lossless(raw []byte) bool {
	// Syntax validation ensures every string escape below has its required
	// bytes. It does not itself reject malformed Unicode surrogate escapes.
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return false
	}
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		if raw[i+1] != 'u' {
			i++
			continue
		}
		code, _ := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		if code >= 0xd800 && code <= 0xdbff {
			next := i + 6
			if next+6 > len(raw) || raw[next] != '\\' || raw[next+1] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[next+2:next+6]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		} else if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		i += 5
	}
	return true
}
