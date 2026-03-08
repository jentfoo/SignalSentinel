package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jentfoo/SignalSentinel/internal/store"
)

type Options struct {
	ConfigPath string
	Session    SessionConfig
}

type Runtime struct {
	mu             sync.RWMutex
	doc            *store.Document
	store          *store.Store
	session        *ScannerSession
	ctx            context.Context
	state          *stateHub
	recordingsPath string
	capabilities   map[ControlIntent]CapabilitySpec
}

type RecordingDeleteFailure struct {
	ID       string
	FilePath string
	Stage    string
	Err      error
}

type RecordingDeleteReport struct {
	Requested int
	Deleted   []string
	Failed    []RecordingDeleteFailure
}

func (r *Runtime) Config() *store.Document {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneDocument(r.doc)
}

func (r *Runtime) Store() *store.Store { return r.store }

func (r *Runtime) RecordingsPath() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.recordingsPath
}

func (r *Runtime) StateSnapshot() RuntimeState {
	if r == nil || r.state == nil {
		return RuntimeState{}
	}
	return r.state.snapshot()
}

func (r *Runtime) SubscribeState(ctx context.Context) <-chan RuntimeState {
	if r == nil || r.state == nil {
		ch := make(chan RuntimeState, 1)
		ch <- RuntimeState{}
		close(ch)
		return ch
	}
	return r.state.subscribe(ctx)
}

func (r *Runtime) Wait() error {
	select {
	case <-r.ctx.Done():
		select {
		case err := <-r.session.Fatal():
			if err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
		default:
		}
		return nil
	case err := <-r.session.Fatal():
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
}

func (r *Runtime) Close() error {
	if r == nil || r.session == nil {
		return nil
	}
	return r.session.Close()
}

func (r *Runtime) EnqueueControl(intent ControlIntent) {
	if r == nil || r.session == nil {
		return
	}
	r.session.EnqueueControl(intent)
}

func (r *Runtime) Recordings() ([]store.RecordingEntry, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("runtime store unavailable")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.doc == nil {
		return nil, errors.New("runtime config missing")
	}
	return append([]store.RecordingEntry(nil), r.doc.State.Recordings...), nil
}

func (r *Runtime) SaveConfig(doc *store.Document) error {
	if r == nil || r.store == nil {
		return errors.New("runtime store unavailable")
	}
	if doc == nil {
		return errors.New("config is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.persistConfigLocked(cloneDocument(doc))
}

func (r *Runtime) UpdateConfig(update func(*store.Document) error) error {
	if r == nil || r.store == nil {
		return errors.New("runtime store unavailable")
	}
	if update == nil {
		return errors.New("config update callback is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	base := cloneDocument(r.doc)
	if base == nil {
		loaded, err := r.store.Load()
		if err != nil {
			return err
		}
		base = cloneDocument(loaded)
	}
	if base == nil {
		return errors.New("runtime config missing")
	}
	if err := update(base); err != nil {
		return err
	}
	return r.persistConfigLocked(base)
}

func (r *Runtime) AppendRecording(entry store.RecordingEntry) error {
	return r.UpdateConfig(func(doc *store.Document) error {
		doc.State.Recordings = append(doc.State.Recordings, entry)
		return nil
	})
}

func (r *Runtime) DeleteRecordingByID(id string) (RecordingDeleteReport, error) {
	return r.DeleteRecordingsByID([]string{id})
}

func (r *Runtime) DeleteRecordingsByID(ids []string) (RecordingDeleteReport, error) {
	report := RecordingDeleteReport{}
	if r == nil || r.store == nil {
		return report, errors.New("runtime store unavailable")
	}

	normalized := uniqueRecordingIDs(ids)
	report.Requested = len(normalized)
	if len(normalized) == 0 {
		return report, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	base := cloneDocument(r.doc)
	if base == nil {
		loaded, err := r.store.Load()
		if err != nil {
			return report, err
		}
		base = cloneDocument(loaded)
	}
	if base == nil {
		return report, errors.New("runtime config missing")
	}

	entries := make(map[string]store.RecordingEntry, len(base.State.Recordings))
	for _, rec := range base.State.Recordings {
		entries[rec.ID] = rec
	}

	deleted := make(map[string]struct{}, len(normalized))
	deletedIDs := make([]string, 0, len(normalized))
	for _, id := range normalized {
		rec, ok := entries[id]
		if !ok {
			report.Failed = append(report.Failed, RecordingDeleteFailure{
				ID:    id,
				Stage: "lookup",
				Err:   errors.New("recording id not found"),
			})
			continue
		}
		if err := deleteRecordingFile(rec.FilePath); err != nil {
			report.Failed = append(report.Failed, RecordingDeleteFailure{
				ID:       rec.ID,
				FilePath: rec.FilePath,
				Stage:    "file_delete",
				Err:      err,
			})
			continue
		}
		deleted[rec.ID] = struct{}{}
		deletedIDs = append(deletedIDs, rec.ID)
	}

	if len(deleted) == 0 {
		return report, nil
	}

	next := base.State.Recordings[:0]
	for _, rec := range base.State.Recordings {
		if _, remove := deleted[rec.ID]; remove {
			continue
		}
		next = append(next, rec)
	}
	base.State.Recordings = next

	if err := r.persistConfigLocked(base); err != nil {
		for _, id := range deletedIDs {
			rec := entries[id]
			report.Failed = append(report.Failed, RecordingDeleteFailure{
				ID:       id,
				FilePath: rec.FilePath,
				Stage:    "metadata_delete",
				Err:      fmt.Errorf("persist metadata removal: %w", err),
			})
		}
		return report, err
	}
	report.Deleted = append(report.Deleted, deletedIDs...)
	return report, nil
}

func (r *Runtime) persistConfigLocked(doc *store.Document) error {
	if r == nil || r.store == nil {
		return errors.New("runtime store unavailable")
	}
	if doc == nil {
		return errors.New("config is required")
	}
	doc.ApplyDefaults()
	if err := doc.Validate(); err != nil {
		return err
	}

	recordingsPath, err := resolveRecordingsPath(doc, r.store)
	if err != nil {
		return err
	}
	if err := ensureDirectoryWritable(recordingsPath); err != nil {
		return fmt.Errorf("validate recordings path: %w", err)
	}
	if err := r.store.Save(doc); err != nil {
		return err
	}
	r.doc = doc
	r.recordingsPath = recordingsPath
	return nil
}

func cloneDocument(doc *store.Document) *store.Document {
	if doc == nil {
		return nil
	}
	clone := *doc
	clone.State.Favorites = append([]store.Favorite(nil), doc.State.Favorites...)
	clone.State.Recordings = append([]store.RecordingEntry(nil), doc.State.Recordings...)
	clone.State.ScanProfiles = make([]store.ScanProfile, len(doc.State.ScanProfiles))
	for i := range doc.State.ScanProfiles {
		profile := doc.State.ScanProfiles[i]
		cp := store.ScanProfile{
			Name:               profile.Name,
			UpdatedAt:          profile.UpdatedAt,
			FavoritesQuickKeys: append([]int(nil), profile.FavoritesQuickKeys...),
			ServiceTypes:       append([]int(nil), profile.ServiceTypes...),
		}
		if len(profile.SystemQuickKeys) > 0 {
			cp.SystemQuickKeys = make(map[string][]int, len(profile.SystemQuickKeys))
			for key, values := range profile.SystemQuickKeys {
				cp.SystemQuickKeys[key] = append([]int(nil), values...)
			}
		}
		if len(profile.DepartmentQuickKeys) > 0 {
			cp.DepartmentQuickKeys = make(map[string][]int, len(profile.DepartmentQuickKeys))
			for key, values := range profile.DepartmentQuickKeys {
				cp.DepartmentQuickKeys[key] = append([]int(nil), values...)
			}
		}
		clone.State.ScanProfiles[i] = cp
	}
	return &clone
}

func StartRuntime(ctx context.Context, opts Options) (*Runtime, error) {
	s := store.New(opts.ConfigPath)
	doc, err := s.Load()
	if err != nil {
		return nil, fmt.Errorf("load config store: %w", err)
	}
	capabilities := DefaultCapabilityRegistry()
	if err := ValidateCapabilityRegistry(capabilities); err != nil {
		return nil, fmt.Errorf("validate capabilities: %w", err)
	}
	if err := doc.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	recordingsPath, err := resolveRecordingsPath(doc, s)
	if err != nil {
		return nil, err
	}
	if err := ensureDirectoryWritable(recordingsPath); err != nil {
		return nil, fmt.Errorf("validate recordings path: %w", err)
	}

	sessionCfg := opts.Session
	sessionCfg.Scanner = doc.Config.Scanner

	hub := newStateHub()
	if err := ValidateCapabilityDefaults(capabilities, hub.snapshot(), false); err != nil {
		return nil, fmt.Errorf("validate capability defaults: %w", err)
	}
	session, err := NewScannerSession(ctx, sessionCfg, hub)
	if err != nil {
		return nil, fmt.Errorf("start scanner session: %w", err)
	}

	return &Runtime{
		doc:            doc,
		store:          s,
		session:        session,
		ctx:            ctx,
		state:          hub,
		recordingsPath: recordingsPath,
		capabilities:   capabilities,
	}, nil
}

func resolveRecordingsPath(doc *store.Document, s *store.Store) (string, error) {
	if doc == nil {
		return "", errors.New("runtime config missing")
	}
	recordingsPath := doc.Config.Storage.RecordingsPath
	if filepath.IsAbs(recordingsPath) {
		return recordingsPath, nil
	}
	storeDir, err := s.Dir()
	if err != nil {
		return "", fmt.Errorf("resolve store directory: %w", err)
	}
	return filepath.Join(storeDir, recordingsPath), nil
}

func ensureDirectoryWritable(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(path, ".write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

func uniqueRecordingIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func deleteRecordingFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
