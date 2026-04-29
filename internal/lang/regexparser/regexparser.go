// Package regexparser provides the bootstrap parser implementation. It walks
// the source line by line and runs a list of named regular expressions
// against each line; matches are emitted as PSI nodes with the configured
// Kind. The parser is deliberately conservative — it recognises the top
// level declarations every editor surfaces in the outline view (packages,
// imports, classes, functions, methods, struct/enum/interface types) and
// returns a partial but useful tree even for malformed input.
//
// Each language adapter wires its own ruleset; the tree-sitter migration
// will swap the implementation behind the parser.Parser interface without
// touching call sites.
package regexparser

import (
	"bufio"
	"bytes"
	"regexp"

	"github.com/zixiao-labs/ines/internal/parser"
	"github.com/zixiao-labs/ines/internal/psi"
)

// Rule maps a regular expression to a PSI Kind. The first capture group of
// Pattern, when present, is used as the element name; otherwise the full
// match is used.
type Rule struct {
	Kind    psi.Kind
	Pattern *regexp.Regexp
}

// MustRule compiles pattern and panics on error. Intended for use during
// package init, where compilation problems must surface as build failures.
func MustRule(kind psi.Kind, pattern string) Rule {
	return Rule{Kind: kind, Pattern: regexp.MustCompile(pattern)}
}

// Parser is a parser.Parser implementation driven by a list of Rules. It is
// safe to share between goroutines because Rules are immutable.
type Parser struct {
	language    string
	rules       []Rule
	lineComment *regexp.Regexp
}

// New constructs a Parser for language using the supplied rules. lineComment
// is optional — when non-nil, lines matching it are skipped before any rule
// is evaluated. This avoids extracting fake declarations out of comment
// blocks.
func New(language string, rules []Rule, lineComment *regexp.Regexp) *Parser {
	return &Parser{language: language, rules: rules, lineComment: lineComment}
}

// Language reports the canonical language id.
func (p *Parser) Language() string { return p.language }

// Parse scans src.Content line by line, applies each rule, and emits a
// matching PSI element under the file root.
func (p *Parser) Parse(src parser.Source) (psi.File, error) {
	file := psi.NewFile(src.Path, p.language, src.Content)
	if len(src.Content) == 0 {
		return file, nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(src.Content))
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<24)
	offset := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		lineLen := len(line)
		// Scanner strips the trailing newline (and any preceding \r for CRLF).
		// Derive the actual line end from the source content to handle both
		// LF and CRLF correctly.
		nextOffset := offset + lineLen
		if nextOffset < len(src.Content) {
			// Find the newline boundary in the original content.
			nlIndex := bytes.IndexByte(src.Content[nextOffset:], '\n')
			if nlIndex >= 0 {
				nextOffset = nextOffset + nlIndex + 1
			} else {
				// No newline found (last line without trailing newline).
				nextOffset = len(src.Content)
			}
		}

		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			offset = nextOffset
			continue
		}
		if p.lineComment != nil && p.lineComment.Match(trimmed) {
			offset = nextOffset
			continue
		}

		for _, rule := range p.rules {
			match := rule.Pattern.FindSubmatchIndex(line)
			if match == nil {
				continue
			}
			name := matchedName(line, match)
			start := offset + match[0]
			end := offset + match[1]
			el := psi.NewElement(rule.Kind, name, psi.Range{Start: start, End: end}, src.Content, p.language)
			file.AddChild(el)
			// Stop after first matching rule to avoid duplicates.
			break
		}
		offset = nextOffset
	}
	return file, scanner.Err()
}

func matchedName(line []byte, match []int) string {
	// match[0:2] is the full match; subsequent pairs are capture groups.
	if len(match) >= 4 && match[2] >= 0 && match[3] >= 0 {
		return string(line[match[2]:match[3]])
	}
	return string(line[match[0]:match[1]])
}
