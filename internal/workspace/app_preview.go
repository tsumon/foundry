package workspace

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/platformfs"
	"html"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// MaxBrowserAppPreviewBytes bounds expanded HTML, including repeated assets.
const MaxBrowserAppPreviewBytes = 10 << 20

const BrowserAppCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; connect-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-src 'none'"

// BrowserAppStorageShim provides an in-memory Storage fallback only when
// opaque-origin iframe sandboxing denies window.localStorage / sessionStorage.
const BrowserAppStorageShim = `<script data-foundry-storage-shim>(function(){function m(){var d=Object.create(null);return{get length(){return Object.keys(d).length},key:function(i){var k=Object.keys(d);return i>=0&&i<k.length?k[i]:null},getItem:function(k){k=String(k);return Object.prototype.hasOwnProperty.call(d,k)?d[k]:null},setItem:function(k,v){d[String(k)]=String(v)},removeItem:function(k){delete d[String(k)]},clear:function(){d=Object.create(null)}};}["localStorage","sessionStorage"].forEach(function(n){try{var s=window[n];if(!s)throw 0;var p="__foundry_probe__";s.setItem(p,"1");s.removeItem(p);}catch(_){try{Object.defineProperty(window,n,{configurable:true,enumerable:true,value:m()});}catch(_){}}});})();</script>`

var ErrBrowserAppAsset = errors.New("unsupported browser application document or asset")

// BrowserAppAssetError carries a bounded diagnostic reason while still matching
// errors.Is(err, ErrBrowserAppAsset).
type BrowserAppAssetError struct {
	Reason string
}

func (e *BrowserAppAssetError) Error() string {
	if e.Reason == "" {
		return ErrBrowserAppAsset.Error()
	}
	return ErrBrowserAppAsset.Error() + ": " + e.Reason
}

func (e *BrowserAppAssetError) Unwrap() error {
	return ErrBrowserAppAsset
}

func browserAppError(reason string) error {
	return &BrowserAppAssetError{Reason: reason}
}

func safeDiagToken(s string) string {
	var b strings.Builder
	for i := 0; i < len(s) && b.Len() < 40; i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '_' || c == '/' {
			b.WriteByte(c)
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// ReadAppFiles returns the exact bounded bytes bound to the selected digest;
// hashing the returned snapshot avoids a validate/read replacement race.
func ReadAppFiles(dir, expected string, contracts ...domain.C4InputContractV2) (map[string][]byte, error) {
	files, err := readTree(dir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	keys := make([]string, 0, len(files))
	for p := range files {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	h := sha256.New()
	inventory := []domain.C4InputFileV2{}
	for _, p := range keys {
		info, err := root.Stat(p)
		if err != nil {
			return nil, err
		}
		mode, err := platformfs.CanonicalMode(info.Mode())
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(h, "%d:%s:%d:%o:", len(p), p, len(files[p]), mode)
		h.Write(files[p])
		sum := sha256.Sum256(files[p])
		inventory = append(inventory, domain.C4InputFileV2{Path: p, Mode: uint32(mode.Perm()), Bytes: int64(len(files[p])), SHA256: hex.EncodeToString(sum[:])})
	}
	if hex.EncodeToString(h.Sum(nil)) != expected {
		return nil, errors.New("candidate digest changed")
	}
	for _, c := range contracts {
		if err := c.ValidateSourceInventory(inventory); err != nil {
			return nil, err
		}
	}
	return files, nil
}
func ZipAppFiles(files map[string][]byte) ([]byte, error) {
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	keys := make([]string, 0, len(files))
	for p := range files {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for _, p := range keys {
		h := &zip.FileHeader{Name: p, Method: zip.Deflate}
		h.SetMode(0644)
		w, err := z.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err = w.Write(files[p]); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

var appHead = regexp.MustCompile(`(?is)^\s*(?:<!doctype\s+html\s*>\s*)?<html(?:\s+(?:lang|dir)\s*=\s*(?:"[a-zA-Z-]+"|'[a-zA-Z-]+'))*\s*>\s*<head\s*>`)
var appTags = regexp.MustCompile(`(?is)<!--.*?-->|<!doctype\s+html\s*>|</?[a-z][a-z0-9:-]*(?:\s+[^<>]*?)?\s*/?>`)
var safeDataImage = regexp.MustCompile(`(?i)^data:image/(?:png|jpeg|gif|webp|svg\+xml)(?:;[a-z0-9=+-]+)*,`)
var externalCSS = regexp.MustCompile(`(?i)url\s*\(|@import|\\`)

// appTag parses the deliberately small classic HTML asset contract. Attribute
// values are decoded before URL checks; duplicate names are rejected.
func appTag(tag string) (string, map[string]string, bool) {
	s := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(tag, "<"), ">"))
	if strings.HasPrefix(s, "/") {
		return strings.ToLower(strings.TrimSpace(s[1:])), nil, true
	}
	i := 0
	for i < len(s) && s[i] != ' ' && s[i] != '\n' && s[i] != '\r' && s[i] != '\t' && s[i] != '/' {
		i++
	}
	name := strings.ToLower(s[:i])
	attrs := map[string]string{}
	for i < len(s) {
		for i < len(s) && strings.ContainsRune(" \t\r\n/", rune(s[i])) {
			i++
		}
		if i == len(s) {
			break
		}
		start := i
		for i < len(s) && !strings.ContainsRune(" \t\r\n=/", rune(s[i])) {
			i++
		}
		if i == start {
			return "", nil, false
		}
		key := strings.ToLower(s[start:i])
		if _, ok := attrs[key]; ok {
			return "", nil, false
		}
		for i < len(s) && strings.ContainsRune(" \t\r\n", rune(s[i])) {
			i++
		}
		value := ""
		if i < len(s) && s[i] == '=' {
			i++
			for i < len(s) && strings.ContainsRune(" \t\r\n", rune(s[i])) {
				i++
			}
			if i == len(s) {
				return "", nil, false
			}
			if s[i] == '"' || s[i] == '\'' {
				quote := s[i]
				i++
				start = i
				for i < len(s) && s[i] != quote {
					i++
				}
				if i == len(s) {
					return "", nil, false
				}
				value = s[start:i]
				i++
			} else {
				start = i
				for i < len(s) && !strings.ContainsRune(" \t\r\n", rune(s[i])) {
					i++
				}
				value = s[start:i]
			}
		}
		attrs[key] = html.UnescapeString(value)
	}
	return name, attrs, true
}
func localAppAsset(files map[string][]byte, ref, extension string) ([]byte, error) {
	if strings.ContainsAny(ref, "\\?#:\x00") || strings.HasPrefix(ref, "/") || ref == "" || ref == ".." || strings.HasPrefix(ref, "../") || strings.Contains(ref, "/../") {
		return nil, browserAppError("asset reference " + safeDiagToken(ref) + " must be a relative path inside src/")
	}
	p := path.Clean(path.Join("src", ref))
	if !strings.HasPrefix(p, "src/") || path.Ext(p) != extension {
		return nil, browserAppError("asset " + safeDiagToken(ref) + " must resolve to a " + extension + " file under src/")
	}
	b, ok := files[p]
	if !ok || len(b) > MaxBrowserAppPreviewBytes || !utf8.Valid(b) {
		return nil, browserAppError("missing or invalid UTF-8 asset " + p)
	}
	return b, nil
}
func BundleBrowserApp(files map[string][]byte) (string, error) {
	raw, ok := files["src/index.html"]
	if !ok || len(raw) > MaxBrowserAppPreviewBytes || !utf8.Valid(raw) {
		return "", browserAppError("missing or invalid UTF-8 src/index.html")
	}
	source := string(raw)
	head := appHead.FindStringIndex(source)
	if head == nil || !strings.Contains(strings.ToLower(source), "</head>") || !strings.Contains(strings.ToLower(source), "</body>") {
		return "", browserAppError("src/index.html must contain valid html, head, and body elements")
	}
	// CSP is controller-authored and is the first head child, followed by the
	// controller-authored opaque-origin storage shim. Candidate tags start at
	// cursor = head[1] so candidate markup cannot spoof shim attributes.
	meta := `<meta http-equiv="Content-Security-Policy" content="` + html.EscapeString(BrowserAppCSP) + `">`
	overhead := len(meta) + len(BrowserAppStorageShim)
	if len(source) > MaxBrowserAppPreviewBytes-overhead {
		return "", browserAppError("bundled HTML exceeds 10 MiB preview limit")
	}
	expanded := len(source) + overhead
	sourceLower := strings.ToLower(source)
	var out strings.Builder
	out.WriteString(source[:head[1]])
	out.WriteString(meta)
	out.WriteString(BrowserAppStorageShim)
	cursor := head[1]
	deferred := []string{}
	matches := appTags.FindAllStringIndex(source, -1)
	for _, pos := range matches {
		if pos[0] < cursor {
			continue
		}
		tag := source[pos[0]:pos[1]]
		if strings.HasPrefix(tag, "<!") {
			continue
		}
		name, a, valid := appTag(tag)
		if !valid {
			return "", browserAppError("malformed HTML tag syntax")
		}
		if strings.HasPrefix(tag, "</") {
			continue
		}
		switch name {
		case "iframe", "frame", "frameset", "object", "embed", "base", "math":
			return "", browserAppError("disallowed element <" + name + ">")
		}
		if name == "meta" && strings.EqualFold(a["http-equiv"], "refresh") {
			return "", browserAppError(`<meta http-equiv="refresh"> is unsupported`)
		}
		for k, v := range a {
			switch k {
			case "srcset", "poster", "background", "data", "action", "formaction", "srcdoc", "ping":
				return "", browserAppError("disallowed attribute " + safeDiagToken(k) + " on <" + safeDiagToken(name) + ">")
			case "href", "xlink:href":
				if name != "link" && !strings.HasPrefix(v, "#") && !(name == "image" && safeDataImage.MatchString(v)) {
					return "", browserAppError("external href on <" + safeDiagToken(name) + "> is unsupported")
				}
			case "src":
				if name != "script" && !(name == "img" && safeDataImage.MatchString(v)) {
					return "", browserAppError("external src on <" + safeDiagToken(name) + "> is unsupported")
				}
			}
		}
		if css, ok := a["style"]; ok && externalCSS.MatchString(css) {
			return "", browserAppError("CSS url(), @import, or backslash escape in inline style is unsupported")
		}
		if name == "style" {
			end := strings.Index(sourceLower[pos[1]:], "</style>")
			if end < 0 || externalCSS.MatchString(source[pos[1]:pos[1]+end]) {
				return "", browserAppError("CSS url(), @import, or backslash escape in <style> is unsupported")
			}
		}
		if name == "link" {
			if !strings.EqualFold(a["rel"], "stylesheet") || strings.TrimSpace(a["href"]) == "" {
				return "", browserAppError(`<link> must use rel="stylesheet" and reference a local .css file under src/`)
			}
			for key, val := range a {
				switch key {
				case "rel", "href", "id", "media", "title":
				case "type":
					if val != "" && !strings.EqualFold(val, "text/css") {
						return "", browserAppError("disallowed link type on <link>")
					}
				default:
					return "", browserAppError("disallowed attribute " + safeDiagToken(key) + " on <link>")
				}
			}
			b, err := localAppAsset(files, a["href"], ".css")
			if err != nil {
				return "", err
			}
			replacementBytes := len("<style></style>") + len(b)
			removedBytes := pos[1] - pos[0]
			if replacementBytes > MaxBrowserAppPreviewBytes-(expanded-removedBytes) {
				return "", browserAppError("bundled HTML exceeds 10 MiB preview limit")
			}
			expanded = expanded - removedBytes + replacementBytes
			if externalCSS.Match(b) || bytes.Contains(bytes.ToLower(b), []byte("</style")) {
				return "", browserAppError("stylesheet CSS url(), @import, backslash escape, or </style is unsupported")
			}
			out.WriteString(source[cursor:pos[0]])
			out.WriteString("<style>" + string(b) + "</style>")
			cursor = pos[1]
		}
		if name == "script" {
			if a["type"] != "" && a["type"] != "text/javascript" {
				return "", browserAppError("non-classic script type (such as module) is unsupported")
			}
			if src, ok := a["src"]; ok {
				for key := range a {
					if key != "src" && key != "type" && key != "defer" {
						return "", browserAppError("disallowed attribute " + safeDiagToken(key) + " on <script src>")
					}
				}
				b, err := localAppAsset(files, src, ".js")
				if err != nil {
					return "", err
				}
				end := strings.Index(sourceLower[pos[1]:], "</script>")
				if end < 0 || strings.TrimSpace(source[pos[1]:pos[1]+end]) != "" {
					return "", browserAppError("<script src> tag must have an empty inline body")
				}
				replacementBytes := len("<script></script>") + len(b)
				removedBytes := pos[1] + end + len("</script>") - pos[0]
				if replacementBytes > MaxBrowserAppPreviewBytes-(expanded-removedBytes) {
					return "", browserAppError("bundled HTML exceeds 10 MiB preview limit")
				}
				expanded = expanded - removedBytes + replacementBytes
				if bytes.Contains(bytes.ToLower(b), []byte("</script")) {
					return "", browserAppError("script file cannot contain </script")
				}
				out.WriteString(source[cursor:pos[0]])
				if _, delayed := a["defer"]; delayed {
					deferred = append(deferred, string(b))
				} else {
					out.WriteString("<script>" + string(b) + "</script>")
				}
				cursor = pos[1] + end + len("</script>")
			} else {
				end := strings.Index(sourceLower[pos[1]:], "</script>")
				if end < 0 {
					return "", browserAppError("unclosed inline <script> tag")
				}
				stop := pos[1] + end + len("</script>")
				out.WriteString(source[cursor:stop])
				cursor = stop
			}
		}
	}
	out.WriteString(source[cursor:])
	result := out.String()
	if len(deferred) > 0 {
		end := strings.LastIndex(strings.ToLower(result), "</body>")
		var scripts strings.Builder
		for _, script := range deferred {
			scripts.WriteString("<script>")
			scripts.WriteString(script)
			scripts.WriteString("</script>")
		}
		result = result[:end] + scripts.String() + result[end:]
	}
	return result, nil
}
