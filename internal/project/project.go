// Package project reads and writes an exported EnvSetup project directory:
// its top-level manifest (envsetup.yaml) and its declared resources
// (resources/<type>.yaml). It knows nothing about what any resource type
// means -- attributes are opaque data bags handed to and from providers.
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jigneshkhatri/envsetup/internal/core"
)

const (
	manifestFileName = "envsetup.yaml"
	resourcesDirName = "resources"
	schemaVersion    = 1
)

// Manifest is the project's top-level envsetup.yaml.
type Manifest struct {
	Name    string `yaml:"name"`
	Version int    `yaml:"version"`
}

// Project is an exported EnvSetup project, loaded from (or destined for)
// disk at Dir.
type Project struct {
	Dir      string
	Manifest Manifest

	resources map[string][]core.ProjectResource // keyed by resource type
}

// New creates a new, empty in-memory project. Call Save to write it to disk.
func New(dir, name string) *Project {
	return &Project{
		Dir:       dir,
		Manifest:  Manifest{Name: name, Version: schemaVersion},
		resources: make(map[string][]core.ProjectResource),
	}
}

// Load reads an existing project from dir.
func Load(dir string) (*Project, error) {
	manifestPath := filepath.Join(dir, manifestFileName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("project: reading %s: %w", manifestPath, err)
	}

	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("project: parsing %s: %w", manifestPath, err)
	}

	p := &Project{Dir: dir, Manifest: manifest, resources: make(map[string][]core.ProjectResource)}

	resourcesDir := filepath.Join(dir, resourcesDirName)
	entries, err := os.ReadDir(resourcesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return p, nil
		}
		return nil, fmt.Errorf("project: reading %s: %w", resourcesDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}

		typ := strings.TrimSuffix(entry.Name(), ".yaml")
		path := filepath.Join(resourcesDir, entry.Name())

		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("project: reading %s: %w", path, err)
		}

		var raw []flatResource
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("project: parsing %s: %w", path, err)
		}

		resources := make([]core.ProjectResource, len(raw))
		for i, r := range raw {
			resources[i] = r.toProjectResource()
		}
		p.resources[typ] = resources
	}

	return p, nil
}

// ResourcesFor returns the desired resources declared for the given
// resource type.
func (p *Project) ResourcesFor(typ string) []core.ProjectResource {
	return p.resources[typ]
}

// SetResourcesFor replaces the desired resources declared for the given
// resource type. Used by export to write newly discovered state into the
// project before Save.
func (p *Project) SetResourcesFor(typ string, resources []core.ProjectResource) {
	p.resources[typ] = resources
}

// Types returns every resource type currently declared in the project,
// ordered deterministically.
func (p *Project) Types() []string {
	types := make([]string, 0, len(p.resources))
	for typ := range p.resources {
		types = append(types, typ)
	}
	sort.Strings(types)
	return types
}

// Save writes the project's manifest and all declared resources to Dir.
func (p *Project) Save() error {
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return fmt.Errorf("project: creating %s: %w", p.Dir, err)
	}

	manifestData, err := yaml.Marshal(p.Manifest)
	if err != nil {
		return fmt.Errorf("project: encoding manifest: %w", err)
	}
	manifestPath := filepath.Join(p.Dir, manifestFileName)
	if err := os.WriteFile(manifestPath, manifestData, 0o644); err != nil {
		return fmt.Errorf("project: writing %s: %w", manifestPath, err)
	}

	resourcesDir := filepath.Join(p.Dir, resourcesDirName)
	if err := os.MkdirAll(resourcesDir, 0o755); err != nil {
		return fmt.Errorf("project: creating %s: %w", resourcesDir, err)
	}

	for typ, resources := range p.resources {
		raw := make([]flatResource, len(resources))
		for i, r := range resources {
			raw[i] = flatResourceFrom(r)
		}

		data, err := yaml.Marshal(raw)
		if err != nil {
			return fmt.Errorf("project: encoding %s resources: %w", typ, err)
		}

		path := filepath.Join(resourcesDir, typ+".yaml")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("project: writing %s: %w", path, err)
		}
	}

	return nil
}

// flatResource is the on-disk shape of a core.ProjectResource: its ID and
// attributes flattened into a single YAML map so resource files read
// naturally, e.g.:
//
//	- id: neovim
//	  provenance: pacman
type flatResource map[string]any

func flatResourceFrom(r core.ProjectResource) flatResource {
	f := make(flatResource, len(r.Attributes)+1)
	for k, v := range r.Attributes {
		f[k] = v
	}
	f["id"] = r.ID
	return f
}

func (f flatResource) toProjectResource() core.ProjectResource {
	id, _ := f["id"].(string)

	attrs := make(map[string]any, len(f))
	for k, v := range f {
		if k == "id" {
			continue
		}
		attrs[k] = v
	}

	return core.ProjectResource{ID: id, Attributes: attrs}
}
