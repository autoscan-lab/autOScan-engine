package domain

import (
	"bytes"
	"os"
)

type Submission struct {
	ID     string   `json:"id"`
	Path   string   `json:"path"`
	CFiles []string `json:"c_files"`
}

func NewSubmission(id, path string, cFiles []string) Submission {
	return Submission{ID: id, Path: path, CFiles: cFiles}
}

// ReadSourceFile reads a submission source file with CRLF and lone CR line endings converted to LF.
func ReadSourceFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	return bytes.ReplaceAll(data, []byte("\r"), []byte("\n")), nil
}
