package frontmatter

import "strings"

// Meta holds parsed YAML frontmatter key-value pairs.
type Meta map[string]string

// Parse splits content delimited by "---" into frontmatter key-value pairs
// and the remaining body. Supports scalar values and YAML list values.
func Parse(content string) (Meta, string) {
	fm := Meta{}
	if !strings.HasPrefix(content, "---") {
		return fm, content
	}

	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return fm, content
	}
	fmBlock := rest[:idx]
	body := rest[idx+4:]

	var currentKey string
	var listBuf []string
	inList := false

	for _, line := range strings.Split(fmBlock, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if inList {
			if strings.HasPrefix(trimmed, "- ") {
				val := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				val = strings.Trim(val, `"'`)
				listBuf = append(listBuf, val)
				continue
			}
			fm[currentKey] = strings.Join(listBuf, "\n")
			inList = false
			listBuf = nil
		}

		if i := strings.Index(trimmed, ":"); i > 0 {
			key := strings.TrimSpace(trimmed[:i])
			val := strings.TrimSpace(trimmed[i+1:])
			currentKey = key
			if val == "" {
				inList = true
				listBuf = nil
				continue
			}
			fm[key] = strings.Trim(val, `"'`)
		}
	}

	if inList && currentKey != "" {
		fm[currentKey] = strings.Join(listBuf, "\n")
	}

	return fm, body
}

// Labels extracts the "labels" frontmatter value as a slice.
// Handles both YAML list form (newline-separated) and inline bracket form.
func (m Meta) Labels() []string {
	raw, ok := m["labels"]
	if !ok || raw == "" {
		return nil
	}

	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		inner := raw[1 : len(raw)-1]
		var out []string
		for _, s := range strings.Split(inner, ",") {
			s = strings.TrimSpace(s)
			s = strings.Trim(s, `"'`)
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}

	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// ParseYAMLList splits a newline-separated value into a string slice.
func ParseYAMLList(raw string) []string {
	var result []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}
