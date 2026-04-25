package domain

type Submission struct {
	ID     string   `json:"id"`
	Path   string   `json:"path"`
	CFiles []string `json:"c_files"`
}

func NewSubmission(id, path string, cFiles []string) Submission {
	return Submission{ID: id, Path: path, CFiles: cFiles}
}
