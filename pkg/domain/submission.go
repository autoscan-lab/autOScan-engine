package domain

type Submission struct {
	ID     string
	Path   string
	CFiles []string
}

func NewSubmission(id, path string, cFiles []string) Submission {
	return Submission{ID: id, Path: path, CFiles: cFiles}
}
