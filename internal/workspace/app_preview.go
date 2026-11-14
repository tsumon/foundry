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

var ErrBrowserAppAsset = errors.New("unsupported browser application document or asset")

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
		return nil, ErrBrowserAppAsset
	}
	p := path.Clean(path.Join("src", ref))
	if !strings.HasPrefix(p, "src/") || path.Ext(p) != extension {
		return nil, ErrBrowserAppAsset
	}
	b, ok := files[p]
	if !ok || len(b) > MaxBrowserAppPreviewBytes || !utf8.Valid(b) {
		return nil, ErrBrowserAppAsset
	}
	return b, nil
}
func BundleBrowserApp(files map[string][]byte) (string, error) {
	raw, ok := files["src/index.html"]
	if !ok || len(raw) > MaxBrowserAppPreviewBytes || !utf8.Valid(raw) {
		return "", ErrBrowserAppAsset
	}
	source := string(raw)
	head := appHead.FindStringIndex(source)
	if head == nil || !strings.Contains(strings.ToLower(source), "</head>") || !strings.Contains(strings.ToLower(source), "</body>") {
		return "", ErrBrowserAppAsset
	}
	// CSP is controller-authored and is the first head child. It is independent
	// of any generated policy or malformed attributes later in the document.
	meta := `<meta http-equiv="Content-Security-Policy" content="` + html.EscapeString(BrowserAppCSP) + `">`
	if len(source) > MaxBrowserAppPreviewBytes-len(meta) {
		return "", ErrBrowserAppAsset
	}
	source = source[:head[1]] + meta + source[head[1]:]
	expanded := len(source)
	sourceLower := strings.ToLower(source)
	var out strings.Builder
	cursor := 0
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
			return "", ErrBrowserAppAsset
		}
		if strings.HasPrefix(tag, "</") {
			continue
		}
		switch name {
		case "iframe", "frame", "frameset", "object", "embed", "base", "math":
			return "", ErrBrowserAppAsset
		}
		if name == "meta" && strings.EqualFold(a["http-equiv"], "refresh") {
			return "", ErrBrowserAppAsset
		}
		for k, v := range a {
			switch k {
			case "srcset", "poster", "background", "data", "action", "formaction", "srcdoc", "ping":
				return "", ErrBrowserAppAsset
			case "href", "xlink:href":
				if name != "link" && !strings.HasPrefix(v, "#") && !(name == "image" && safeDataImage.MatchString(v)) {
					return "", ErrBrowserAppAsset
				}
			case "src":
				if name != "script" && !(name == "img" && safeDataImage.MatchString(v)) {
					return "", ErrBrowserAppAsset
				}
			}
		}
		if css, ok := a["style"]; ok && externalCSS.MatchString(css) {
			return "", ErrBrowserAppAsset
		}
		if name == "style" {
			end := strings.Index(sourceLower[pos[1]:], "</style>")
			if end < 0 || externalCSS.MatchString(source[pos[1]:pos[1]+end]) {
				return "", ErrBrowserAppAsset
			}
		}
		if name == "link" {
			if !strings.EqualFold(a["rel"], "stylesheet") || len(a) != 2 {
				return "", ErrBrowserAppAsset
			}
			b, err := localAppAsset(files, a["href"], ".css")
			if err != nil {
				return "", err
			}
			replacementBytes := len("<style></style>") + len(b)
			removedBytes := pos[1] - pos[0]
			if replacementBytes > MaxBrowserAppPreviewBytes-(expanded-removedBytes) {
				return "", ErrBrowserAppAsset
			}
			expanded = expanded - removedBytes + replacementBytes
			if externalCSS.Match(b) || bytes.Contains(bytes.ToLower(b), []byte("</style")) {
				return "", ErrBrowserAppAsset
			}
			out.WriteString(source[cursor:pos[0]])
			out.WriteString("<style>" + string(b) + "</style>")
			cursor = pos[1]
		}
		if name == "script" {
			if a["type"] != "" && a["type"] != "text/javascript" {
				return "", ErrBrowserAppAsset
			}
			if src, ok := a["src"]; ok {
				for key := range a {
					if key != "src" && key != "type" && key != "defer" {
						return "", ErrBrowserAppAsset
					}
				}
				b, err := localAppAsset(files, src, ".js")
				if err != nil {
					return "", ErrBrowserAppAsset
				}
				end := strings.Index(sourceLower[pos[1]:], "</script>")
				if end < 0 || strings.TrimSpace(source[pos[1]:pos[1]+end]) != "" {
					return "", ErrBrowserAppAsset
				}
				replacementBytes := len("<script></script>") + len(b)
				removedBytes := pos[1] + end + len("</script>") - pos[0]
				if replacementBytes > MaxBrowserAppPreviewBytes-(expanded-removedBytes) {
					return "", ErrBrowserAppAsset
				}
				expanded = expanded - removedBytes + replacementBytes
				if bytes.Contains(bytes.ToLower(b), []byte("</script")) {
					return "", ErrBrowserAppAsset
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
					return "", ErrBrowserAppAsset
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
