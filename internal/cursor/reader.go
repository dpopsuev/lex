package cursor

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dpopsuev/lex/internal/frontmatter"
)

type Rule struct {
	Path        string   `json:"path"`
	Description string   `json:"description,omitempty"`
	AlwaysApply bool     `json:"always_apply,omitempty"`
	Globs       []string `json:"globs,omitempty"`
	Body        string   `json:"body"`
}

type Skill struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Body        string `json:"body"`
}

func ReadRules(root string) ([]Rule, error) {
	rulesDir := filepath.Join(root, ".cursor", "rules")
	if _, err := os.Stat(rulesDir); os.IsNotExist(err) {
		return nil, nil
	}

	var rules []Rule
	err := filepath.Walk(rulesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".mdc") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		r := Rule{Path: rel}
		fm, body := frontmatter.Parse(string(data))
		r.Body = strings.TrimSpace(body)
		r.Description = fm["description"]
		if v, ok := fm["alwaysApply"]; ok && (v == "true" || v == "True") {
			r.AlwaysApply = true
		}
		if g, ok := fm["globs"]; ok {
			r.Globs = frontmatter.ParseYAMLList(g)
		}
		rules = append(rules, r)
		return nil
	})
	return rules, err
}

func ReadSkills(root string) ([]Skill, error) {
	skillsDir := filepath.Join(root, ".cursor", "skills")
	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		return nil, nil
	}

	var skills []Skill
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillFile := filepath.Join(skillsDir, e.Name(), "SKILL.md")
		data, err := os.ReadFile(skillFile)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, skillFile)
		sk := Skill{Path: rel}
		fm, body := frontmatter.Parse(string(data))
		sk.Body = strings.TrimSpace(body)
		sk.Name = fm["name"]
		sk.Description = fm["description"]
		skills = append(skills, sk)
	}
	return skills, nil
}

