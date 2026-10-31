package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fullsend-ai/fullsend/internal/sandbox"
	"github.com/fullsend-ai/fullsend/internal/skill"
)

const (
	// personasFileName is the manifest's file name on the host (in the run
	// directory) and in the sandbox (next to output-schema.json).
	personasFileName = "personas.json"
	// personasEnvVar names the manifest path for skills and host scripts.
	personasEnvVar = "FULLSEND_PERSONAS_FILE"
	// personasManifestVersion is the manifest schema version.
	personasManifestVersion = 1
)

// remotePersonasPath is the manifest's path inside the sandbox. It sits
// under .fullsend/ outside the target repo checkout, so a PR cannot plant
// one.
var remotePersonasPath = sandbox.SandboxWorkspace + "/.fullsend/" + personasFileName

// personaManifest is the personas.json document (version 1).
type personaManifest struct {
	Version  int              `json:"version"`
	Personas []personaEntry   `json:"personas"`
	Removed  []removedPersona `json:"removed"`
}

// personaEntry describes one staged sub-agents/*.md file. The optional
// fields are copied verbatim from frontmatter and omitted when absent; the
// runner checks their shape only and gives them no meaning.
type personaEntry struct {
	Name            string    `json:"name"`
	Skill           string    `json:"skill"`
	Path            string    `json:"path"`
	Origin          string    `json:"origin"`
	SHA256          string    `json:"sha256"`
	Description     *string   `json:"description,omitempty"`
	Role            *string   `json:"role,omitempty"`
	When            *string   `json:"when,omitempty"`
	Categories      *[]string `json:"categories,omitempty"`
	Always          *bool     `json:"always,omitempty"`
	FailureSeverity *string   `json:"failure_severity,omitempty"`
}

// removedPersona is a sub-agents/ file a null override removed.
type removedPersona struct {
	Skill string `json:"skill"`
	Path  string `json:"path"`
}

// buildPersonaManifest lists every sub-agents/*.md file (no recursion)
// under each staged skill directory, the same discovery rule the pi and
// codex runtimes use.
func buildPersonaManifest(staged []stagedSkill) (personaManifest, error) {
	m := personaManifest{
		Version:  personasManifestVersion,
		Personas: []personaEntry{},
		Removed:  []removedPersona{},
	}
	// persona name → the skill and file that first declared it. Only a
	// name shared by two different skills is an error: inside one skill an
	// override may reuse another file's bytes (and so its frontmatter
	// name), and each entry is still told apart by its path.
	type declared struct{ skill, file string }
	seen := map[string]declared{}
	for _, s := range staged {
		skillName := skill.SandboxName(s.Dir)
		for _, rel := range s.Removed {
			if strings.HasPrefix(rel, "sub-agents/") {
				m.Removed = append(m.Removed, removedPersona{Skill: skillName, Path: rel})
			}
		}
		entries, err := os.ReadDir(filepath.Join(s.Dir, "sub-agents"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return personaManifest{}, fmt.Errorf("%s: reading %s/sub-agents: %w", personasFileName, skillName, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			rel := "sub-agents/" + e.Name()
			file := skillName + "/" + rel
			data, err := os.ReadFile(filepath.Join(s.Dir, "sub-agents", e.Name()))
			if err != nil {
				return personaManifest{}, fmt.Errorf("%s: %s: %w", personasFileName, file, err)
			}
			p, err := personaFromFile(file, data)
			if err != nil {
				return personaManifest{}, err
			}
			if p.Name == "" {
				p.Name = strings.TrimSuffix(e.Name(), ".md")
			}
			if prev, dup := seen[p.Name]; !dup {
				seen[p.Name] = declared{skill: skillName, file: file}
			} else if prev.skill != skillName {
				return personaManifest{}, fmt.Errorf("%s: duplicate persona name %q in %s and %s", personasFileName, p.Name, prev.file, file)
			}
			sum := sha256.Sum256(data)
			p.Skill = skillName
			p.Path = rel
			p.Origin = s.origin(rel)
			p.SHA256 = hex.EncodeToString(sum[:])
			m.Personas = append(m.Personas, p)
		}
	}
	sort.Slice(m.Personas, func(i, j int) bool {
		a, b := m.Personas[i], m.Personas[j]
		if a.Skill != b.Skill {
			return a.Skill < b.Skill
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Path < b.Path
	})
	sort.Slice(m.Removed, func(i, j int) bool {
		if m.Removed[i].Skill != m.Removed[j].Skill {
			return m.Removed[i].Skill < m.Removed[j].Skill
		}
		return m.Removed[i].Path < m.Removed[j].Path
	})
	return m, nil
}

// personaFromFile reads the frontmatter fields the manifest copies. A
// file without frontmatter yields an empty entry (its name falls back to
// the file stem); frontmatter that does not parse, or a copied field of
// the wrong type, is an error naming the file.
func personaFromFile(file string, data []byte) (personaEntry, error) {
	var p personaEntry
	block, ok := skill.FrontmatterBlock(data)
	if !ok {
		if skill.HasFrontmatterFence(data) {
			return p, fmt.Errorf("%s: %s: frontmatter does not parse: no closing ---", personasFileName, file)
		}
		return p, nil
	}
	var fm map[string]any
	if err := yaml.Unmarshal(block, &fm); err != nil {
		return p, fmt.Errorf("%s: %s: frontmatter does not parse: %w", personasFileName, file, err)
	}
	str := func(field string) (*string, error) {
		v, ok := fm[field]
		if !ok {
			return nil, nil
		}
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s: %s: frontmatter field %q must be a string", personasFileName, file, field)
		}
		return &s, nil
	}
	name, err := str("name")
	if err != nil {
		return p, err
	}
	if name != nil {
		p.Name = strings.TrimSpace(*name)
	}
	for field, dst := range map[string]**string{
		"description":      &p.Description,
		"role":             &p.Role,
		"when":             &p.When,
		"failure_severity": &p.FailureSeverity,
	} {
		if *dst, err = str(field); err != nil {
			return p, err
		}
	}
	if v, ok := fm["always"]; ok {
		b, ok := v.(bool)
		if !ok {
			return p, fmt.Errorf("%s: %s: frontmatter field %q must be a bool", personasFileName, file, "always")
		}
		p.Always = &b
	}
	if v, ok := fm["categories"]; ok {
		list, ok := v.([]any)
		if !ok {
			return p, fmt.Errorf("%s: %s: frontmatter field %q must be a list of strings", personasFileName, file, "categories")
		}
		cats := make([]string, 0, len(list))
		for _, c := range list {
			s, ok := c.(string)
			if !ok {
				return p, fmt.Errorf("%s: %s: frontmatter field %q must be a list of strings", personasFileName, file, "categories")
			}
			cats = append(cats, s)
		}
		p.Categories = &cats
	}
	return p, nil
}

// writePersonaManifest builds the manifest for the staged skills and
// writes it to runDir/personas.json, returning the absolute path.
func writePersonaManifest(staged []stagedSkill, runDir string) (string, error) {
	m, err := buildPersonaManifest(staged)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding %s: %w", personasFileName, err)
	}
	dest, err := filepath.Abs(filepath.Join(runDir, personasFileName))
	if err != nil {
		return "", fmt.Errorf("writing %s: %w", personasFileName, err)
	}
	if err := os.WriteFile(dest, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", personasFileName, err)
	}
	return dest, nil
}

// withPersonasFile appends FULLSEND_PERSONAS_FILE=<runDir>/personas.json
// to a host script environment, dropping any earlier value (for example
// one runner_env set) so the runner's path always wins.
func withPersonasFile(env []string, runDir string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, personasEnvVar+"=") {
			continue
		}
		out = append(out, e)
	}
	return append(out, personasEnvVar+"="+filepath.Join(runDir, personasFileName))
}
