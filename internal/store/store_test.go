package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func DefaultDocument() *Document {
	d := &Document{}
	d.ApplyDefaults()
	return d
}

func TestStoreLoadAndSave(t *testing.T) {
	t.Run("load_missing_returns_defaults", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runtime.yaml")
		s := New(path)

		doc, err := s.Load()
		require.NoError(t, err)

		assert.Equal(t, CurrentVersion, doc.Version)
		assert.Equal(t, 50536, doc.Config.Scanner.ControlPort)
		assert.Equal(t, 554, doc.Config.Scanner.RTSPPort)
		assert.Equal(t, 10, doc.Config.Recording.HangTimeSeconds)
		assert.Empty(t, doc.State.Favorites)
	})

	t.Run("save_then_load_roundtrip", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runtime.yaml")
		s := New(path)

		doc := DefaultDocument()
		doc.Config.Scanner.IP = "192.168.1.50"
		doc.Config.Storage.RecordingsPath = "/tmp/recs"
		doc.State.Favorites = []Favorite{{Name: "Local PD", Frequency: "155.190"}}

		err := s.Save(doc)
		require.NoError(t, err)

		loaded, err := s.Load()
		require.NoError(t, err)

		assert.Equal(t, "192.168.1.50", loaded.Config.Scanner.IP)
		assert.Equal(t, "/tmp/recs", loaded.Config.Storage.RecordingsPath)
		require.Len(t, loaded.State.Favorites, 1)
		assert.Equal(t, "Local PD", loaded.State.Favorites[0].Name)
	})

	t.Run("empty_path_uses_default_config_path_and_creates_directory", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		s := New("")

		doc := DefaultDocument()
		doc.Config.Scanner.IP = "10.0.0.10"

		err := s.Save(doc)
		require.NoError(t, err)

		defaultPath := filepath.Join(home, ".sigsentinel", "config.yaml")
		_, err = os.Stat(defaultPath)
		require.NoError(t, err)

		loaded, err := s.Load()
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.10", loaded.Config.Scanner.IP)
	})
}

func TestDocumentValidate(t *testing.T) {
	t.Run("valid_document", func(t *testing.T) {
		doc := DefaultDocument()
		doc.Config.Scanner.IP = "10.0.1.10"

		err := doc.Validate()
		assert.NoError(t, err)
	})

	t.Run("invalid_scanner_ip", func(t *testing.T) {
		doc := DefaultDocument()
		doc.Config.Scanner.IP = "not-an-ip"

		err := doc.Validate()
		assert.Error(t, err)
	})

	t.Run("invalid_port", func(t *testing.T) {
		doc := DefaultDocument()
		doc.Config.Scanner.IP = "10.0.2.10"
		doc.Config.Scanner.ControlPort = 70000

		err := doc.Validate()
		assert.Error(t, err)
	})
}

func TestRuntimeRadioStateIsNotPersisted(t *testing.T) {
	t.Run("yaml_contains_only_config_and_state", func(t *testing.T) {
		type payload struct {
			Document          `yaml:",inline"`
			RuntimeRadioState RuntimeRadioState `yaml:"-"`
		}

		p := payload{
			Document:          *DefaultDocument(),
			RuntimeRadioState: RuntimeRadioState{Connected: true, Frequency: "155.190", SquelchOpen: true},
		}
		p.Config.Scanner.IP = "10.0.1.10"

		data, err := yaml.Marshal(p)
		require.NoError(t, err)

		assert.NotContains(t, string(data), "connected")
		assert.NotContains(t, string(data), "squelch")
		assert.Contains(t, string(data), "config:")
		assert.Contains(t, string(data), "state:")
	})
}
