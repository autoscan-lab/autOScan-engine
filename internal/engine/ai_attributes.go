package engine

import (
	"bytes"
	"regexp"
)

var unusedAttributeSyntax = regexp.MustCompile(`^__attribute__\s*\(\s*\(\s*(?:unused|__unused__)\s*\)\s*\)`)

type attributeSpan struct{ start, end int }

// Mask only recognized GNU unused attributes, preserving source offsets for the C parser.
func compatibleUnusedAttributes(content []byte) ([]byte, []attributeSpan) {
	masked := bytes.Clone(content)
	var spans []attributeSpan
	lineStart := true
	for i := 0; i < len(content); {
		if content[i] == '\n' {
			lineStart = true
			i++
			continue
		}
		if lineStart && (content[i] == ' ' || content[i] == '\t') {
			i++
			continue
		}
		if lineStart && content[i] == '#' {
			for i < len(content) {
				if content[i] == '\n' && (i == 0 || content[i-1] != '\\') {
					break
				}
				i++
			}
			continue
		}
		lineStart = false
		if i+1 < len(content) && content[i] == '/' && content[i+1] == '/' {
			for i < len(content) && content[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(content) && content[i] == '/' && content[i+1] == '*' {
			i += 2
			for i+1 < len(content) && !(content[i] == '*' && content[i+1] == '/') {
				if content[i] == '\n' {
					lineStart = true
				}
				i++
			}
			i = min(i+2, len(content))
			continue
		}
		if content[i] == '"' || content[i] == '\'' {
			quote := content[i]
			i++
			for i < len(content) {
				if content[i] == '\\' {
					i = min(i+2, len(content))
					continue
				}
				if content[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		}
		if (i == 0 || !cIdentifierByte(content[i-1])) && content[i] == '_' {
			if match := unusedAttributeSyntax.FindIndex(content[i:]); match != nil {
				end := i + match[1]
				spans = append(spans, attributeSpan{i, end})
				for j := i; j < end; j++ {
					if masked[j] != '\n' {
						masked[j] = ' '
					}
				}
				i = end
				continue
			}
		}
		i++
	}
	return masked, spans
}

func cIdentifierByte(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value >= 128
}
