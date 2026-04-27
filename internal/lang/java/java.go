// Package java registers the Java language adapter.
package java

import (
	"regexp"

	"github.com/zixiao-labs/ines/internal/lang"
	"github.com/zixiao-labs/ines/internal/lang/regexparser"
	"github.com/zixiao-labs/ines/internal/psi"
)

func init() {
	rules := []regexparser.Rule{
		regexparser.MustRule(psi.KindPackage, `^\s*package\s+([A-Za-z_][A-Za-z0-9_.]*)`),
		regexparser.MustRule(psi.KindImport, `^\s*import\s+(?:static\s+)?([A-Za-z_][A-Za-z0-9_.*]*)`),
		regexparser.MustRule(psi.KindClass, `^\s*(?:public\s+|private\s+|protected\s+|abstract\s+|final\s+|static\s+)*class\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindInterface, `^\s*(?:public\s+|private\s+|protected\s+|abstract\s+|static\s+)*interface\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindEnum, `^\s*(?:public\s+|private\s+|protected\s+|static\s+)*enum\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindMethod, `^\s*(?:public\s+|private\s+|protected\s+|static\s+|final\s+|abstract\s+|synchronized\s+)+[A-Za-z_][\w<>\[\],\s\?]*\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`),
	}
	parser := regexparser.New("java", rules, regexp.MustCompile(`^\s*//`))
	lang.Register(&lang.Adapter{
		Language:   "java",
		Extensions: []string{".java"},
		Parser:     parser,
	})
}
