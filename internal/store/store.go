package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Store struct {
	path string
}

func New(path string) *Store {
	return &Store{path: path}
}

func (s *Store) filePath() (string, error) {
	if s.path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		s.path = filepath.Join(home, ".sigsentinel", "config.yaml")
	}
	return s.path, nil
}

func (s *Store) Load() (*Document, error) {
	path, err := s.filePath()
	if err != nil {
		return nil, err
	}

	doc := &Document{}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			doc.ApplyDefaults()
			return doc, nil
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, doc); err != nil {
		return nil, err
	}
	doc.ApplyDefaults() // fill any new defaults in
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	return doc, nil
}

func (s *Store) Save(doc *Document) error {
	if doc == nil {
		return nil
	}

	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}

	path, err := s.filePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "tmpcfg-*.yaml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	} else if err := tmp.Close(); err != nil {
		return err
	} else if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}
