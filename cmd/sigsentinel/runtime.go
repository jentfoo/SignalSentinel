package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jentfoo/SignalSentinel/internal/gui"
	"github.com/jentfoo/SignalSentinel/internal/store"
)

func deepCopyIntSliceMap(src map[string][]int) map[string][]int {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string][]int, len(src))
	for k, v := range src {
		dst[k] = append([]int(nil), v...)
	}
	return dst
}

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

type ProfileScopeSelector struct {
	FavoritesTag int
	SystemTag    int
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

func (r *Runtime) ExecuteControl(intent ControlIntent, params ControlParams) error {
	if r == nil || r.session == nil {
		return errors.New("runtime session unavailable")
	}
	return r.session.ExecuteControl(intent, params)
}

func (r *Runtime) ReadScanScope(favoritesTag, systemTag int) (gui.ScanScopeSnapshot, error) {
	if r == nil || r.session == nil {
		return gui.ScanScopeSnapshot{}, errors.New("runtime session unavailable")
	}
	return r.session.ReadScanScope(favoritesTag, systemTag)
}

func (r *Runtime) ScanProfiles() ([]store.ScanProfile, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("runtime store unavailable")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.doc == nil {
		return nil, errors.New("runtime config missing")
	}
	return cloneScanProfiles(r.doc.State.ScanProfiles), nil
}

func (r *Runtime) SaveScanProfile(profile store.ScanProfile) error {
	if r == nil || r.store == nil {
		return errors.New("runtime store unavailable")
	}
	return r.UpdateConfig(func(doc *store.Document) error {
		normalized, err := normalizeScanProfile(profile)
		if err != nil {
			return err
		}
		normalized.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

		for i := range doc.State.ScanProfiles {
			if strings.EqualFold(strings.TrimSpace(doc.State.ScanProfiles[i].Name), normalized.Name) {
				existing := doc.State.ScanProfiles[i]
				updated := store.ScanProfile{
					Name:               normalized.Name,
					UpdatedAt:          normalized.UpdatedAt,
					FavoritesQuickKeys: append([]int(nil), normalized.FavoritesQuickKeys...),
					ServiceTypes:       append([]int(nil), normalized.ServiceTypes...),
				}
				if len(existing.SystemQuickKeys) > 0 || len(normalized.SystemQuickKeys) > 0 {
					updated.SystemQuickKeys = make(map[string][]int, len(existing.SystemQuickKeys)+len(normalized.SystemQuickKeys))
					for key, values := range existing.SystemQuickKeys {
						canonicalKey := strings.TrimSpace(key)
						parsed, convErr := strconv.Atoi(canonicalKey)
						if convErr == nil && parsed >= 0 && parsed <= 99 {
							canonicalKey = strconv.Itoa(parsed)
						}
						updated.SystemQuickKeys[canonicalKey] = append([]int(nil), values...)
					}
					for key, values := range normalized.SystemQuickKeys {
						updated.SystemQuickKeys[key] = append([]int(nil), values...)
					}
				}
				if len(existing.DepartmentQuickKeys) > 0 || len(normalized.DepartmentQuickKeys) > 0 {
					updated.DepartmentQuickKeys = make(map[string][]int, len(existing.DepartmentQuickKeys)+len(normalized.DepartmentQuickKeys))
					for key, values := range existing.DepartmentQuickKeys {
						canonicalKey := strings.TrimSpace(key)
						parts := strings.Split(canonicalKey, ":")
						if len(parts) == 2 {
							favTag, favErr := strconv.Atoi(strings.TrimSpace(parts[0]))
							sysTag, sysErr := strconv.Atoi(strings.TrimSpace(parts[1]))
							if favErr == nil && sysErr == nil && favTag >= 0 && favTag <= 99 && sysTag >= 0 && sysTag <= 99 {
								canonicalKey = fmt.Sprintf("%d:%d", favTag, sysTag)
							}
						}
						updated.DepartmentQuickKeys[canonicalKey] = append([]int(nil), values...)
					}
					for key, values := range normalized.DepartmentQuickKeys {
						updated.DepartmentQuickKeys[key] = append([]int(nil), values...)
					}
				}
				doc.State.ScanProfiles[i] = updated
				return nil
			}
		}
		doc.State.ScanProfiles = append(doc.State.ScanProfiles, normalized)
		return nil
	})
}

func (r *Runtime) DeleteScanProfile(name string) error {
	if r == nil || r.store == nil {
		return errors.New("runtime store unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("scan profile name is required")
	}
	return r.UpdateConfig(func(doc *store.Document) error {
		next := doc.State.ScanProfiles[:0]
		for _, profile := range doc.State.ScanProfiles {
			if strings.EqualFold(strings.TrimSpace(profile.Name), name) {
				continue
			}
			next = append(next, profile)
		}
		doc.State.ScanProfiles = next
		return nil
	})
}

func (r *Runtime) ApplyScanProfile(name string, scope ProfileScopeSelector) error {
	if r == nil || r.session == nil {
		return errors.New("runtime session unavailable")
	}
	if err := validateQuickKeyTag("favorites quick key", scope.FavoritesTag); err != nil {
		return err
	}
	if err := validateQuickKeyTag("system quick key", scope.SystemTag); err != nil {
		return err
	}
	profile, err := r.findScanProfile(name)
	if err != nil {
		return err
	}

	if len(profile.FavoritesQuickKeys) > 0 {
		if err := r.ExecuteControl(IntentSetFavoritesQuickKeys, ControlParams{
			QuickKeyValues: append([]int(nil), profile.FavoritesQuickKeys...),
		}); err != nil {
			return err
		}
	}
	if len(profile.SystemQuickKeys) > 0 {
		var values []int
		found := false
		canonicalKey := strconv.Itoa(scope.FavoritesTag)
		if selected, ok := profile.SystemQuickKeys[canonicalKey]; ok {
			values = selected
			found = true
		} else {
			keys := make([]string, 0, len(profile.SystemQuickKeys))
			for key := range profile.SystemQuickKeys {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				tag, convErr := strconv.Atoi(strings.TrimSpace(key))
				if convErr != nil || tag != scope.FavoritesTag {
					continue
				}
				values = profile.SystemQuickKeys[key]
				found = true
				break
			}
		}
		if found {
			if err := r.ExecuteControl(IntentSetSystemQuickKeys, ControlParams{
				ScopeFavoritesTag: scope.FavoritesTag,
				QuickKeyValues:    append([]int(nil), values...),
			}); err != nil {
				return err
			}
		}
	}
	if len(profile.DepartmentQuickKeys) > 0 {
		var values []int
		found := false
		canonicalKey := fmt.Sprintf("%d:%d", scope.FavoritesTag, scope.SystemTag)
		if selected, ok := profile.DepartmentQuickKeys[canonicalKey]; ok {
			values = selected
			found = true
		} else {
			keys := make([]string, 0, len(profile.DepartmentQuickKeys))
			for key := range profile.DepartmentQuickKeys {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				parts := strings.Split(strings.TrimSpace(key), ":")
				if len(parts) != 2 {
					continue
				}
				favTag, favErr := strconv.Atoi(strings.TrimSpace(parts[0]))
				sysTag, sysErr := strconv.Atoi(strings.TrimSpace(parts[1]))
				if favErr != nil || sysErr != nil || favTag != scope.FavoritesTag || sysTag != scope.SystemTag {
					continue
				}
				values = profile.DepartmentQuickKeys[key]
				found = true
				break
			}
		}
		if found {
			if err := r.ExecuteControl(IntentSetDepartmentQuickKeys, ControlParams{
				ScopeFavoritesTag: scope.FavoritesTag,
				ScopeSystemTag:    scope.SystemTag,
				QuickKeyValues:    append([]int(nil), values...),
			}); err != nil {
				return err
			}
		}
	}
	if len(profile.ServiceTypes) > 0 {
		if err := r.ExecuteControl(IntentSetServiceTypes, ControlParams{
			ServiceTypes: append([]int(nil), profile.ServiceTypes...),
		}); err != nil {
			return err
		}
	}
	return nil
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
			Name:                profile.Name,
			UpdatedAt:           profile.UpdatedAt,
			FavoritesQuickKeys:  append([]int(nil), profile.FavoritesQuickKeys...),
			ServiceTypes:        append([]int(nil), profile.ServiceTypes...),
			SystemQuickKeys:     deepCopyIntSliceMap(profile.SystemQuickKeys),
			DepartmentQuickKeys: deepCopyIntSliceMap(profile.DepartmentQuickKeys),
		}
		clone.State.ScanProfiles[i] = cp
	}
	return &clone
}

func cloneScanProfiles(values []store.ScanProfile) []store.ScanProfile {
	if len(values) == 0 {
		return nil
	}
	out := make([]store.ScanProfile, len(values))
	for i := range values {
		out[i] = values[i]
		out[i].FavoritesQuickKeys = append([]int(nil), values[i].FavoritesQuickKeys...)
		out[i].ServiceTypes = append([]int(nil), values[i].ServiceTypes...)
		out[i].SystemQuickKeys = deepCopyIntSliceMap(values[i].SystemQuickKeys)
		out[i].DepartmentQuickKeys = deepCopyIntSliceMap(values[i].DepartmentQuickKeys)
	}
	return out
}

func normalizeScanProfile(profile store.ScanProfile) (store.ScanProfile, error) {
	out := store.ScanProfile{
		Name: strings.TrimSpace(profile.Name),
	}
	if out.Name == "" {
		return store.ScanProfile{}, errors.New("scan profile name is required")
	}

	favorites, err := validateBinaryValues(profile.FavoritesQuickKeys, 100, "favorites quick keys")
	if err != nil {
		return store.ScanProfile{}, err
	}
	out.FavoritesQuickKeys = favorites

	serviceTypes, err := validateBinaryValues(profile.ServiceTypes, 47, "service types")
	if err != nil {
		return store.ScanProfile{}, err
	}
	out.ServiceTypes = serviceTypes

	if len(profile.SystemQuickKeys) > 0 {
		out.SystemQuickKeys = make(map[string][]int, len(profile.SystemQuickKeys))
		for key, values := range profile.SystemQuickKeys {
			cleanKey := strings.TrimSpace(key)
			if cleanKey == "" {
				return store.ScanProfile{}, errors.New("system quick key profile key is required")
			}
			parsed, err := strconv.Atoi(cleanKey)
			if err != nil {
				return store.ScanProfile{}, fmt.Errorf("invalid system quick key profile key %q", cleanKey)
			}
			if err := validateQuickKeyTag("system quick key profile key", parsed); err != nil {
				return store.ScanProfile{}, err
			}
			normalized, normErr := validateBinaryValues(values, 100, "system quick keys")
			if normErr != nil {
				return store.ScanProfile{}, normErr
			}
			out.SystemQuickKeys[strconv.Itoa(parsed)] = normalized
		}
	}
	if len(profile.DepartmentQuickKeys) > 0 {
		out.DepartmentQuickKeys = make(map[string][]int, len(profile.DepartmentQuickKeys))
		for key, values := range profile.DepartmentQuickKeys {
			cleanKey := strings.TrimSpace(key)
			if cleanKey == "" {
				return store.ScanProfile{}, errors.New("department quick key profile key is required")
			}
			parts := strings.Split(cleanKey, ":")
			if len(parts) != 2 {
				return store.ScanProfile{}, fmt.Errorf("invalid department quick key profile key %q", cleanKey)
			}
			favTag, err := strconv.Atoi(strings.TrimSpace(parts[0]))
			if err != nil {
				return store.ScanProfile{}, fmt.Errorf("invalid department quick key profile key %q", cleanKey)
			}
			sysTag, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return store.ScanProfile{}, fmt.Errorf("invalid department quick key profile key %q", cleanKey)
			}
			if err := validateQuickKeyTag("department quick key profile favorites tag", favTag); err != nil {
				return store.ScanProfile{}, err
			}
			if err := validateQuickKeyTag("department quick key profile system tag", sysTag); err != nil {
				return store.ScanProfile{}, err
			}
			normalized, normErr := validateBinaryValues(values, 100, "department quick keys")
			if normErr != nil {
				return store.ScanProfile{}, normErr
			}
			out.DepartmentQuickKeys[fmt.Sprintf("%d:%d", favTag, sysTag)] = normalized
		}
	}

	return out, nil
}

func (r *Runtime) findScanProfile(name string) (store.ScanProfile, error) {
	if r == nil || r.store == nil {
		return store.ScanProfile{}, errors.New("runtime store unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return store.ScanProfile{}, errors.New("scan profile name is required")
	}
	profiles, err := r.ScanProfiles()
	if err != nil {
		return store.ScanProfile{}, err
	}
	for _, profile := range profiles {
		if strings.EqualFold(strings.TrimSpace(profile.Name), name) {
			return profile, nil
		}
	}
	return store.ScanProfile{}, fmt.Errorf("scan profile not found: %s", name)
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
