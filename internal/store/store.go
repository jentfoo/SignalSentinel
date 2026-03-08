package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/renameio/v2"
	"gopkg.in/yaml.v3"
)

type Store struct {
	path     string
	pathOnce sync.Once
	pathErr  error
}

func New(path string) *Store {
	return &Store{path: path}
}

func (s *Store) filePath() (string, error) {
	s.pathOnce.Do(func() {
		if s.path != "" {
			return
		}
		home, err := os.UserHomeDir()
		if err != nil {
			s.pathErr = fmt.Errorf("resolve user home: %w", err)
			return
		}
		s.path = filepath.Join(home, ".sigsentinel", "config.yaml")
	})
	if s.pathErr != nil {
		return "", s.pathErr
	}
	return s.path, nil
}

func (s *Store) Dir() (string, error) {
	p, err := s.filePath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(p), nil
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
	return renameio.WriteFile(path, data, 0o644)
}

func (s *Store) AppendRecording(entry RecordingEntry) error {
	doc, err := s.Load()
	if err != nil {
		return err
	}
	doc.State.Recordings = append(doc.State.Recordings, entry)
	return s.Save(doc)
}
