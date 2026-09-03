package scan

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// LoadPublications reads a publications.jsonl file (one Publication per line)
// written by the scanner. Blank lines are skipped. A malformed line is a hard
// error — a corrupt record must not be silently dropped.
func LoadPublications(path string) ([]Publication, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<27)
	var out []Publication
	line := 0
	for sc.Scan() {
		line++
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var p Publication
		if err := json.Unmarshal(sc.Bytes(), &p); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, line, err)
		}
		out = append(out, p)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return out, nil
}
