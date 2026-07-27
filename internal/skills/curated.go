package skills

import (
	"embed"
	"sort"
	"strings"
)

//go:embed curated/*.md
var curatedFS embed.FS

var curated = map[string]string{}

func init() {
	entries, err := curatedFS.ReadDir("curated")
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		data, err := curatedFS.ReadFile("curated/" + e.Name())
		if err != nil {
			continue
		}
		curated[name] = string(data)
	}
}

// Curated returns the embedded SKILL.md content for a curated skill name.
func Curated(name string) (string, bool) {
	c, ok := curated[strings.TrimSpace(name)]
	return c, ok
}

// CuratedNames returns the sorted list of available curated skill names.
func CuratedNames() []string {
	names := make([]string, 0, len(curated))
	for n := range curated {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
