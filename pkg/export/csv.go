package export

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/felitrejos/autoscan-engine/pkg/domain"
)

// Export a report to CSV format
func CSV(report domain.RunReport, outputDir string) (string, error) {
	filename := filepath.Join(outputDir, "report.csv")
	file, err := os.Create(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	header := []string{
		"submission_id",
		"status",
		"compile_ok",
		"compile_timeout",
		"exit_code",
		"compile_time_ms",
		"banned_count",
		"banned_functions",
		"first_error_line",
	}
	if err := writer.Write(header); err != nil {
		return "", err
	}

	for _, r := range report.Results {
		var bannedFuncs []string
		for fn := range r.Scan.HitsByFunction {
			bannedFuncs = append(bannedFuncs, fn)
		}

		firstError := ""
		if !r.Compile.OK && r.Compile.Stderr != "" {
			lines := strings.Split(r.Compile.Stderr, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" && (strings.Contains(line, "error:") || strings.Contains(line, "Error:")) {
					firstError = line
					if len(firstError) > 100 {
						firstError = firstError[:97] + "..."
					}
					break
				}
			}
			if firstError == "" {
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line != "" {
						firstError = line
						if len(firstError) > 100 {
							firstError = firstError[:97] + "..."
						}
						break
					}
				}
			}
		}

		row := []string{
			r.Submission.ID,
			string(r.Status),
			strconv.FormatBool(r.Compile.OK),
			strconv.FormatBool(r.Compile.TimedOut),
			strconv.Itoa(r.Compile.ExitCode),
			strconv.FormatInt(r.Compile.DurationMs, 10),
			strconv.Itoa(r.Scan.TotalHits()),
			strings.Join(bannedFuncs, ","),
			firstError,
		}

		if err := writer.Write(row); err != nil {
			return "", err
		}
	}

	return filename, nil
}
