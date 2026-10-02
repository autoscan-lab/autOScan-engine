package engine

import (
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

var temporaryPathLiteral = regexp.MustCompile(`^(?:u8|[LuU])?"/tmp(?:/[^"\r\n]*)?"`)

var posixSemaphoreCalls = map[string]bool{
	"sem_init": true, "sem_destroy": true, "sem_open": true, "sem_close": true, "sem_unlink": true,
	"sem_wait": true, "sem_trywait": true, "sem_timedwait": true, "sem_post": true, "sem_getvalue": true,
}

func (a *styleAnalysis) courseDeclaration(node *sitter.Node) {
	if node.Type() == "type_definition" {
		for i := 0; i < int(node.ChildCount()); i++ {
			if node.FieldNameForChild(i) != "declarator" {
				continue
			}
			name := nameOf(node.Child(i), a.content)
			if strings.HasSuffix(name, "_t") && name != "pthread_t" && name != "key_t" {
				a.idiom("custom _t typedef names", node.Child(i))
			}
		}
	}
	if node.Type() == "field_declaration" {
		for i := 0; i < int(node.ChildCount()); i++ {
			if node.FieldNameForChild(i) == "declarator" {
				a.declaredName(node.Child(i))
			}
		}
	}
}
