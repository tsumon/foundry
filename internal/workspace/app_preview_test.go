package workspace

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestBrowserAppPreviewBundlesAndIsolates(t *testing.T) {
	files := map[string][]byte{"src/index.html": []byte(`<!doctype html><html lang="en"><head><title>Keep title</title><link rel="stylesheet" href="style.css"></head><body><main>OK</main><script src="app.js"></script></body></html>`), "src/style.css": []byte(`body{color:red}`), "src/app.js": []byte(`document.body.dataset.ready='yes'`)}
	html, err := BundleBrowserApp(files)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "<head><meta http-equiv=") || !strings.Contains(html, "Keep title") || !strings.Contains(html, "color:red") || strings.Contains(html, `src="app.js"`) {
		t.Fatal(html)
	}
	for _, bad := range []string{`<link rel="stylesheet" href="https://evil.invalid/x.css">`, `<script src="../secret.js"></script>`, `<iframe src="about:blank"></iframe>`, `<meta http-equiv="refresh" content="0;url=https://evil.invalid">`} {
		old := files["src/index.html"]
		files["src/index.html"] = []byte(strings.Replace(string(old), "<title>Keep title</title>", bad, 1))
		if _, err = BundleBrowserApp(files); err == nil {
			t.Fatalf("accepted %s", bad)
		}
		files["src/index.html"] = old
	}
}

func TestBrowserAppPreviewSupportsIconsDataAndDeferredScripts(t *testing.T) {
	files := map[string][]byte{"src/index.html": []byte(`<!doctype html><html lang="en"><head><title>App</title><script defer type="text/javascript" src="app.js"></script></head><body><main><svg viewBox="0 0 10 10"><path d="M0 0L10 10"/></svg><img alt="Icon" src="data:image/png;base64,aGVsbG8="><p>https://example.invalid is visible text</p></main><script>const example="<iframe>"; // ordinary comment</script></body></html>`), "src/app.js": []byte(`document.body.dataset.ready='yes';`)}
	html, err := BundleBrowserApp(files)
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Index(html, "document.body.dataset.ready")
	if script < strings.Index(html, "<main>") || !strings.Contains(html, "<svg ") || !strings.Contains(html, "data:image/png") || !strings.Contains(html, "https://example.invalid") {
		t.Fatal(html)
	}
	for _, bad := range []string{`<style>@import 'https://evil.invalid/a.css';</style>`, `<img src="https://evil.invalid/a.png">`, `<svg><image href="https://evil.invalid/a.png"/></svg>`} {
		original := files["src/index.html"]
		files["src/index.html"] = []byte(strings.Replace(string(original), "<title>App</title>", bad, 1))
		if _, err = BundleBrowserApp(files); err == nil {
			t.Fatal("accepted external asset", bad)
		}
		files["src/index.html"] = original
	}
}

func TestBrowserAppPreviewRejectsRepeatedAssetExpansion(t *testing.T) {
	for _, deferAttr := range []string{"", " defer"} {
		html := `<!doctype html><html><head><title>Bounded</title></head><body><main>OK</main>` + strings.Repeat(`<script`+deferAttr+` src="app.js"></script>`, 12) + `</body></html>`
		files := map[string][]byte{"src/index.html": []byte(html), "src/app.js": []byte(strings.Repeat(" ", MaxBrowserAppPreviewBytes/8))}
		if _, err := BundleBrowserApp(files); err == nil {
			t.Fatal("repeated asset expansion accepted", deferAttr)
		}
	}
	html := `<!doctype html><html><head><title>Bounded</title>` + strings.Repeat(`<link rel="stylesheet" href="style.css">`, 12) + `</head><body><main>OK</main></body></html>`
	if _, err := BundleBrowserApp(map[string][]byte{"src/index.html": []byte(html), "src/style.css": []byte(strings.Repeat(" ", MaxBrowserAppPreviewBytes/8))}); err == nil {
		t.Fatal("repeated CSS expansion accepted")
	}
}
func TestBrowserAppPreviewPreservesDeferredScriptSemantics(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable for owned fixture")
	}
	files := map[string][]byte{"src/index.html": []byte(`<!doctype html><html><head><title>Order</title><script defer src="a.js"></script><script defer src="b.js"></script></head><body><main>OK</main></body></html>`), "src/a.js": []byte(`"use strict"; globalThis.order=['a'];`), "src/b.js": []byte(`implicitValue=7; order.push('b');`)}
	html, err := BundleBrowserApp(files)
	if err != nil {
		t.Fatal(err)
	}
	matches := regexp.MustCompile(`(?s)<script>(.*?)</script>`).FindAllStringSubmatch(html, -1)
	if len(matches) != 2 {
		t.Fatalf("wanted separate deferred elements: %s", html)
	}
	scripts := []string{matches[0][1], matches[1][1]}
	raw, _ := json.Marshal(scripts)
	cmd := exec.Command(node, "-e", `const vm=require('node:vm');const assert=require('node:assert/strict');const context=vm.createContext({});for(const script of JSON.parse(process.argv[1]))vm.runInContext(script,context);assert.equal(context.implicitValue,7);assert.equal(context.order.join(','),'a,b');`, string(raw))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("classic script semantics changed: %v\n%s", err, out)
	}
}
