package instance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/BurntSushi/toml"
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// ValidateName keeps names safe for file paths, socket paths and (on Linux)
// interface names.
func ValidateName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid instance name %q: use lowercase letters, digits and '-', max 31 chars, starting with a letter or digit", name)
	}
	return nil
}

// Store persists instances as one TOML file per instance under Root.
type Store struct {
	Root   string // multibird config root (0700)
	RunDir string // per-OS run dir for sockets/pids
}

func (s *Store) params(i *Instance) Params { return i.DeriveParams(s.Root, s.RunDir) }

// Save writes the instance TOML with credential-grade permissions.
func (s *Store) Save(i *Instance) error {
	p := s.params(i)
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return fmt.Errorf("creating instance dir %s: %w", p.Dir, err)
	}
	f, err := os.OpenFile(p.TOMLPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("writing %s: %w", p.TOMLPath, err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(i); err != nil {
		return fmt.Errorf("encoding %s: %w", p.TOMLPath, err)
	}
	return nil
}

// Load reads one instance by name.
func (s *Store) Load(name string) (*Instance, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	path := filepath.Join(s.Root, name, "instance.toml")
	var i Instance
	if _, err := toml.DecodeFile(path, &i); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no instance named %q — run `multibird list` to see instances, or `multibird add %s ...` to create it", name, name)
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return &i, nil
}

// List returns all instances sorted by name.
func (s *Store) List() ([]*Instance, error) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", s.Root, err)
	}
	var out []*Instance
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.Root, e.Name(), "instance.toml")); err != nil {
			continue
		}
		i, err := s.Load(e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

// NextIndex allocates the lowest index not used by any existing instance.
func (s *Store) NextIndex() (int, error) {
	list, err := s.List()
	if err != nil {
		return 0, err
	}
	used := map[int]bool{}
	for _, i := range list {
		used[i.Index] = true
	}
	for n := 0; ; n++ {
		if !used[n] {
			return n, nil
		}
	}
}

// Remove deletes multibird's state for the instance. When purge is true it
// also removes netbird's config dir contents (the whole instance dir).
func (s *Store) Remove(i *Instance, purge bool) error {
	p := s.params(i)
	if purge {
		if err := os.RemoveAll(p.Dir); err != nil {
			return fmt.Errorf("purging %s: %w", p.Dir, err)
		}
		return nil
	}
	if err := os.Remove(p.TOMLPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", p.TOMLPath, err)
	}
	return nil
}
