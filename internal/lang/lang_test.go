package lang_test

import (
	"testing"

	"github.com/zixiao-labs/ines/internal/lang"
	_ "github.com/zixiao-labs/ines/internal/lang/c"
	_ "github.com/zixiao-labs/ines/internal/lang/golang"
	_ "github.com/zixiao-labs/ines/internal/lang/java"
	_ "github.com/zixiao-labs/ines/internal/lang/rust"
	_ "github.com/zixiao-labs/ines/internal/lang/swift"
	_ "github.com/zixiao-labs/ines/internal/lang/typescript"
)

func TestRegistryHasEverySupportedLanguage(t *testing.T) {
	want := []string{"c" /* alias of cpp via .c */, "cpp", "go", "java", "rust", "swift", "typescript"}
	have := map[string]bool{}
	for _, a := range lang.All() {
		have[a.Language] = true
	}
	for _, lang := range []string{"cpp", "go", "java", "rust", "swift", "typescript"} {
		if !have[lang] {
			t.Errorf("missing adapter %q (registered: %v)", lang, have)
		}
	}
	_ = want
}

func TestByPathDispatchesByExtension(t *testing.T) {
	cases := map[string]string{
		"main.go":      "go",
		"App.tsx":      "typescript",
		"index.js":     "typescript",
		"impl.rs":      "rust",
		"Foo.java":     "java",
		"View.swift":   "swift",
		"engine.cpp":   "cpp",
		"engine.h":     "cpp",
	}
	for path, lng := range cases {
		got := lang.ByPath(path)
		if got == nil {
			t.Errorf("no adapter for %q", path)
			continue
		}
		if got.Language != lng {
			t.Errorf("%q -> %q want %q", path, got.Language, lng)
		}
	}
}
