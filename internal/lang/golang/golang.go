// Package golang registers the Go language adapter.
package golang

import (
	"regexp"

	"github.com/zixiao-labs/ines/internal/lang"
	"github.com/zixiao-labs/ines/internal/lang/regexparser"
	"github.com/zixiao-labs/ines/internal/psi"
)

func init() {
	rules := []regexparser.Rule{
		regexparser.MustRule(psi.KindPackage, `^\s*package\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindImport, `^\s*import\s+(?:"([^"]+)"|([A-Za-z_][A-Za-z0-9_]*))`),
		regexparser.MustRule(psi.KindStruct, `^\s*type\s+([A-Za-z_][A-Za-z0-9_]*)\s+struct\b`),
		regexparser.MustRule(psi.KindInterface, `^\s*type\s+([A-Za-z_][A-Za-z0-9_]*)\s+interface\b`),
		regexparser.MustRule(psi.KindEnum, `^\s*type\s+([A-Za-z_][A-Za-z0-9_]*)\s+(?:int|int8|int16|int32|int64|uint|uint8|uint16|uint32|uint64|string)\b`),
		regexparser.MustRule(psi.KindMethod, `^\s*func\s*\([^)]*\)\s*([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindFunction, `^\s*func\s+([A-Za-z_][A-Za-z0-9_]*)\s*[\[(]`),
		regexparser.MustRule(psi.KindVariable, `^\s*var\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindVariable, `^\s*const\s+([A-Za-z_][A-Za-z0-9_]*)`),
	}
	lang.Register(&lang.Adapter{
		Language:   "go",
		Extensions: []string{".go"},
		Parser:     regexparser.New("go", rules, regexp.MustCompile(`^\s*//`)),
	})
}
