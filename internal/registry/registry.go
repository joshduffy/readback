// Package registry is the single list of modules readback knows about. It drives
// `capabilities`, `schema`, `search`, and `install-skills`, so an agent can discover
// the tool locally without fetching docs.
package registry

import (
	"sort"
	"strings"
)

type Status string

const (
	StatusPlanned Status = "planned"
	StatusBeta    Status = "beta"
	StatusStable  Status = "stable"
)

type Module struct {
	Name      string                            `json:"name"`
	Summary   string                            `json:"summary"`
	Status    Status                            `json:"status"`
	Milestone string                            `json:"milestone"`
	Keywords  []string                          `json:"keywords"`
	Schemas   map[string]map[string]interface{} `json:"schemas,omitempty"`
}

var modules = map[string]Module{}

// Register is called from each module's init. Duplicate names panic at startup,
// which is the correct failure for a build-time registry.
func Register(m Module) {
	if _, dup := modules[m.Name]; dup {
		panic("readback: duplicate module " + m.Name)
	}
	modules[m.Name] = m
}

func All() []Module {
	out := make([]Module, 0, len(modules))
	for _, m := range modules {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func Get(name string) (Module, bool) {
	m, ok := modules[name]
	return m, ok
}

// Search is a case-insensitive substring match over name, summary, and keywords.
func Search(term string) []Module {
	term = strings.ToLower(term)
	var out []Module
	for _, m := range All() {
		hay := strings.ToLower(m.Name + " " + m.Summary + " " + strings.Join(m.Keywords, " "))
		if strings.Contains(hay, term) {
			out = append(out, m)
		}
	}
	return out
}
