package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jentfoo/SignalSentinel/internal/audio/ingest"
	"github.com/jentfoo/SignalSentinel/internal/audio/recording"
	"github.com/jentfoo/SignalSentinel/internal/gui"
	"github.com/jentfoo/SignalSentinel/internal/sds200"
	"github.com/jentfoo/SignalSentinel/internal/store"
)

func main() {
	log.SetFlags(log.Ltime)

	opts, err := parseFlags(os.Args[1:], os.Stdout)
	if err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(2)
	}

	if err := run(opts); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

func run(opts cliFlags) error {
	if opts.ShowHelp {
		return nil // already shown
	}
	if err := persistCLIOverrides(opts); err != nil {
		return fmt.Errorf("startup: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runtime, err := StartRuntime(ctx, Options{
		ConfigPath: opts.ConfigPath,
	})
	if err != nil {
		return fmt.Errorf("startup: %w", err)
	}
	defer func() { _ = runtime.Close() }()

	audioSession, recorder, audioErrs, err := startAudioPipeline(ctx, runtime)
	if err != nil {
		return fmt.Errorf("startup: %w", err)
	}
	defer func() {
		if closeErr := recorder.Close(); closeErr != nil {
			log.Printf("recorder close error: %v", closeErr)
		}
	}()
	defer func() { _ = audioSession.Close() }()

	toGUIRuntimeState := func(status sds200.RuntimeStatus) gui.RuntimeState {
		caps := EvaluateCapabilities(runtime.capabilities, RuntimeState{Scanner: status}, false)
		recStatus := recorder.Snapshot()
		return gui.RuntimeState{
			Scanner: gui.ScannerStatus{
				Connected:     status.Connected,
				Mode:          status.Mode,
				LifecycleMode: gui.DeriveLifecycleMode(status.Connected, status.Hold, status.Mode),
				ViewScreen:    status.ViewScreen,
				Frequency:     status.Frequency,
				System:        status.System,
				Department:    status.Department,
				Channel:       status.Channel,
				Talkgroup:     status.Talkgroup,
				Hold:          status.Hold,
				Signal:        status.Signal,
				SquelchOpen:   status.SquelchOpen,
				Active:        sds200.IsTransmissionActive(status),
				Mute:          status.Mute,
				Volume:        status.Volume,
				Squelch:       status.Squelch,
				UpdatedAt:     status.UpdatedAt,
				LastSource:    status.LastSource,
				CanHoldTarget: status.HoldTarget.Keyword != "" && status.HoldTarget.Arg1 != "",
				Avoided:       status.Avoided,
				AvoidKnown:    status.AvoidKnown,
				Capabilities:  buildGUICapabilities(caps),
			},
			Recording: gui.RecordingStatus{
				Active:    recStatus.Active,
				StartedAt: recStatus.StartedAt,
				Trigger:   recStatus.Trigger,
				Manual:    recStatus.Manual,
			},
		}
	}

	initialSettings := gui.Settings{}
	if cfg := runtime.Config(); cfg != nil {
		initialSettings = gui.Settings{
			ScannerIP:       cfg.Config.Scanner.IP,
			RecordingsPath:  cfg.Config.Storage.RecordingsPath,
			HangTimeSeconds: cfg.Config.Recording.HangTimeSeconds,
			Activity: gui.ActivitySettings{
				StartDebounceMS: cfg.Config.Activity.StartDebounceMS,
				EndDebounceMS:   cfg.Config.Activity.EndDebounceMS,
				MinActivityMS:   cfg.Config.Activity.MinActivityMS,
			},
		}
	}

	fatalErrs := superviseSubsystems(ctx, runtime, audioErrs)
	guiErr := gui.Run(ctx, gui.Dependencies{
		Title:           "SignalSentinel",
		InitialState:    toGUIRuntimeState(runtime.StateSnapshot().Scanner),
		InitialSettings: initialSettings,
		SubscribeState: func(subCtx context.Context) <-chan gui.RuntimeState {
			src := runtime.SubscribeState(subCtx)
			out := make(chan gui.RuntimeState, 1)
			go func() {
				defer close(out)
				for {
					select {
					case <-subCtx.Done():
						return
					case status, ok := <-src:
						if !ok {
							return
						}
						publishLatestGUIState(out, toGUIRuntimeState(status.Scanner))
					}
				}
			}()
			return out
		},
		ExecuteControl: func(request gui.ControlRequest) gui.ControlResult {
			return executeGUIControl(runtime, request)
		},
		LoadScanScope: func(favoritesTag, systemTag int) (gui.ScanScopeSnapshot, error) {
			return runtime.ReadScanScope(favoritesTag, systemTag)
		},
		LoadScanProfiles: func() ([]gui.ScanProfile, error) {
			profiles, err := runtime.ScanProfiles()
			if err != nil {
				return nil, err
			}
			out := make([]gui.ScanProfile, 0, len(profiles))
			for _, profile := range profiles {
				out = append(out, gui.ScanProfile{
					Name:                profile.Name,
					FavoritesQuickKeys:  append([]int(nil), profile.FavoritesQuickKeys...),
					ServiceTypes:        append([]int(nil), profile.ServiceTypes...),
					UpdatedAt:           profile.UpdatedAt,
					SystemQuickKeys:     deepCopyIntSliceMap(profile.SystemQuickKeys),
					DepartmentQuickKeys: deepCopyIntSliceMap(profile.DepartmentQuickKeys),
				})
			}
			sort.SliceStable(out, func(i, j int) bool {
				return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
			})
			return out, nil
		},
		SaveScanProfile: func(profile gui.ScanProfile) error {
			return runtime.SaveScanProfile(store.ScanProfile{
				Name:                strings.TrimSpace(profile.Name),
				FavoritesQuickKeys:  append([]int(nil), profile.FavoritesQuickKeys...),
				ServiceTypes:        append([]int(nil), profile.ServiceTypes...),
				SystemQuickKeys:     deepCopyIntSliceMap(profile.SystemQuickKeys),
				DepartmentQuickKeys: deepCopyIntSliceMap(profile.DepartmentQuickKeys),
			})
		},
		DeleteScanProfile: func(name string) error {
			return runtime.DeleteScanProfile(name)
		},
		ApplyScanProfile: func(name string, favoritesTag int, systemTag int) error {
			return runtime.ApplyScanProfile(name, ProfileScopeSelector{
				FavoritesTag: favoritesTag,
				SystemTag:    systemTag,
			})
		},
		StartRecording: func() error {
			status := runtime.StateSnapshot().Scanner
			if err := recorder.StartManual(status, time.Now()); err != nil {
				return err
			}
			if runtime.state != nil {
				runtime.state.publish(runtime.StateSnapshot())
			}
			return nil
		},
		StopRecording: func() error {
			if err := recorder.StopManual(time.Now()); err != nil {
				return err
			}
			if runtime.state != nil {
				runtime.state.publish(runtime.StateSnapshot())
			}
			return nil
		},
		LoadRecordings: func() ([]gui.Recording, error) {
			entries, err := runtime.Recordings()
			if err != nil {
				return nil, err
			}
			var missingIDs []string
			for _, entry := range entries {
				path := strings.TrimSpace(entry.FilePath)
				if path == "" {
					continue
				}
				if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
					missingIDs = append(missingIDs, entry.ID)
				}
			}
			if len(missingIDs) > 0 {
				if _, deleteErr := runtime.DeleteRecordingsByID(missingIDs); deleteErr != nil {
					return nil, fmt.Errorf("reconcile missing recordings: %w", deleteErr)
				}
				entries, err = runtime.Recordings()
				if err != nil {
					return nil, err
				}
			}

			items := make([]gui.Recording, 0, len(entries))
			for _, entry := range entries {
				items = append(items, gui.Recording{
					ID:        entry.ID,
					StartedAt: entry.StartedAt,
					EndedAt:   entry.EndedAt,
					Duration:  entry.Duration,
					Frequency: entry.Frequency,
					System:    entry.System,
					Channel:   entry.Channel,
					Talkgroup: entry.Talkgroup,
					FilePath:  entry.FilePath,
					FileSize:  entry.FileSize,
					Trigger:   entry.Trigger,
				})
			}
			sort.SliceStable(items, func(i, j int) bool {
				return items[i].StartedAt > items[j].StartedAt
			})
			return items, nil
		},
		DeleteRecordings: func(ids []string) (gui.DeleteReport, error) {
			report, err := runtime.DeleteRecordingsByID(ids)
			out := gui.DeleteReport{
				Requested: report.Requested,
				Deleted:   append([]string(nil), report.Deleted...),
				Failed:    make([]gui.DeleteReportFailure, 0, len(report.Failed)),
			}
			for _, failure := range report.Failed {
				msg := failure.Err.Error()
				if failure.FilePath != "" {
					msg = fmt.Sprintf("%s (%s)", msg, failure.FilePath)
				}
				out.Failed = append(out.Failed, gui.DeleteReportFailure{
					ID:      failure.ID,
					Stage:   failure.Stage,
					Message: msg,
				})
			}
			return out, err
		},
		SaveSettings: func(settings gui.Settings) error {
			if err := runtime.UpdateConfig(func(doc *store.Document) error {
				doc.Config.Scanner.IP = strings.TrimSpace(settings.ScannerIP)
				doc.Config.Storage.RecordingsPath = strings.TrimSpace(settings.RecordingsPath)
				if settings.HangTimeChanged {
					if settings.HangTimeSeconds < 1 {
						return errors.New("hang-time must be >= 1 second")
					}
					doc.Config.Recording.HangTimeSeconds = settings.HangTimeSeconds
				}
				return nil
			}); err != nil {
				return err
			}
			return recorder.UpdateOutputDir(runtime.RecordingsPath())
		},
		Fatal: fatalErrs,
	})
	if guiErr != nil && !errors.Is(guiErr, context.Canceled) {
		return guiErr
	}
	return nil
}

func executeGUIControl(runtime *Runtime, request gui.ControlRequest) gui.ControlResult {
	result := gui.ControlResult{
		Intent:  request.Intent,
		Success: false,
		At:      time.Now().UTC(),
	}
	intent, params, action, err := mapGUIControlRequest(request)
	if err != nil {
		result.Action = "Invalid Request"
		result.Command = "-"
		result.Message = "request rejected"
		result.RawReason = err.Error()
		result.RetryHint = "Fix request values and retry."
		return result
	}
	spec, ok := runtime.capabilities[intent]
	if ok {
		result.Command = spec.Command
	} else {
		result.Command = "-"
	}
	result.Action = action

	capabilities := EvaluateCapabilities(runtime.capabilities, runtime.StateSnapshot(), false)
	if cap, ok := capabilities[intent]; ok && !cap.Available {
		result.Message = "operation unavailable"
		result.RawReason = cap.DisabledReason
		result.RetryHint = "Adjust scanner mode/state and retry."
		return result
	}

	if err := runtime.ExecuteControl(intent, params); err != nil {
		message, hint, unsupported := classifyControlError(err)
		result.Message = message
		result.RawReason = err.Error()
		result.RetryHint = hint
		result.Unsupported = unsupported
		return result
	}
	result.Success = true
	result.Message = "command executed"
	result.RawReason = "-"
	result.RetryHint = "-"
	return result
}

func classifyControlError(err error) (message string, retryHint string, unsupported bool) {
	if err == nil {
		return "command failed", "Retry.", false
	}
	reason := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(reason, "unsupported"):
		return "operation not supported", "Use an alternative control or verify scanner firmware/mode.", true
	case strings.Contains(reason, "must be in range"), strings.Contains(reason, "must contain"), strings.Contains(reason, "must be 0 or 1"):
		return "invalid input", "Adjust control values and retry.", false
	case strings.Contains(reason, "unavailable"), strings.Contains(reason, "hold target"):
		return "operation unavailable in current state", "Wait for a valid hold target or change scan mode, then retry.", false
	case strings.Contains(reason, "resync failed"):
		return "scope changed but refresh failed", "Retry once. If it persists, reconnect scanner session.", false
	default:
		return "command failed", "Retry. If this repeats, check scanner connection and logs.", false
	}
}

func mapGUIControlRequest(request gui.ControlRequest) (ControlIntent, ControlParams, string, error) {
	switch request.Intent {
	case gui.IntentHoldCurrent:
		return IntentHold, ControlParams{}, "Hold", nil
	case gui.IntentReleaseHold:
		return IntentResumeScan, ControlParams{}, "Release Hold", nil
	case gui.IntentNext:
		return IntentNext, ControlParams{}, "Next", nil
	case gui.IntentPrevious:
		return IntentPrevious, ControlParams{}, "Previous", nil
	case gui.IntentJumpNumberTag:
		return IntentJumpNumberTag, ControlParams{
			FavoritesTag: request.NumberTag.Favorites,
			SystemTag:    request.NumberTag.System,
			ChannelTag:   request.NumberTag.Channel,
		}, "Jump Number Tag", nil
	case gui.IntentQuickSearchHold:
		return IntentQuickSearchHold, ControlParams{
			FrequencyHz: request.FrequencyHz,
		}, "Quick Search Hold", nil
	case gui.IntentJumpMode:
		return IntentJumpMode, ControlParams{
			JumpMode:  request.JumpMode,
			JumpIndex: request.JumpIndex,
		}, "Jump Mode", nil
	case gui.IntentAvoid:
		return IntentAvoid, ControlParams{}, "Avoid", nil
	case gui.IntentUnavoid:
		return IntentUnavoid, ControlParams{}, "Unavoid", nil
	case gui.IntentSetVolume:
		return IntentSetVolume, ControlParams{Volume: request.Volume}, "Set Volume", nil
	case gui.IntentSetSquelch:
		return IntentSetSquelch, ControlParams{Squelch: request.Squelch}, "Set Squelch", nil
	case gui.IntentSetFQK:
		return IntentSetFavoritesQuickKeys, ControlParams{
			QuickKeyValues: append([]int(nil), request.QuickKeyValues...),
		}, "Set Favorites Quick Keys", nil
	case gui.IntentSetSQK:
		return IntentSetSystemQuickKeys, ControlParams{
			ScopeFavoritesTag: request.ScopeFavoritesTag,
			QuickKeyValues:    append([]int(nil), request.QuickKeyValues...),
		}, "Set System Quick Keys", nil
	case gui.IntentSetDQK:
		return IntentSetDepartmentQuickKeys, ControlParams{
			ScopeFavoritesTag: request.ScopeFavoritesTag,
			ScopeSystemTag:    request.ScopeSystemTag,
			QuickKeyValues:    append([]int(nil), request.QuickKeyValues...),
		}, "Set Department Quick Keys", nil
	case gui.IntentSetServiceTypes:
		return IntentSetServiceTypes, ControlParams{
			ServiceTypes: append([]int(nil), request.ServiceTypes...),
		}, "Set Service Types", nil
	default:
		return "", ControlParams{}, "", fmt.Errorf("unsupported control intent: %s", request.Intent)
	}
}

func buildGUICapabilities(items map[ControlIntent]CapabilityAvailability) map[gui.ControlIntent]gui.ControlCapability {
	if len(items) == 0 {
		return nil
	}
	needed := []ControlIntent{
		IntentHold,
		IntentResumeScan,
		IntentNext,
		IntentPrevious,
		IntentJumpNumberTag,
		IntentQuickSearchHold,
		IntentJumpMode,
		IntentAvoid,
		IntentUnavoid,
		IntentSetVolume,
		IntentSetSquelch,
		IntentSetFavoritesQuickKeys,
		IntentSetSystemQuickKeys,
		IntentSetDepartmentQuickKeys,
		IntentSetServiceTypes,
	}
	out := make(map[gui.ControlIntent]gui.ControlCapability, len(needed))
	for _, intent := range needed {
		item, ok := items[intent]
		if !ok {
			continue
		}
		out[gui.ControlIntent(intent)] = gui.ControlCapability{
			Available:      item.Available,
			DisabledReason: item.DisabledReason,
		}
	}
	return out
}

func superviseSubsystems(ctx context.Context, runtime *Runtime, audioErrs <-chan error) <-chan error {
	out := make(chan error, 1)
	runtimeDone := make(chan error, 1)
	go func() {
		runtimeDone <- runtime.Wait()
	}()

	go func() {
		defer close(out)
		runtimeWait := runtimeDone
		for {
			select {
			case <-ctx.Done():
				return
			case err := <-runtimeWait:
				runtimeWait = nil
				if err == nil || errors.Is(err, context.Canceled) {
					continue
				}
				out <- err
				return
			case err := <-audioErrs:
				if err == nil || errors.Is(err, context.Canceled) {
					continue
				}
				out <- err
				return
			}
		}
	}()

	return out
}

func publishLatestGUIState(out chan gui.RuntimeState, state gui.RuntimeState) {
	select {
	case out <- state:
	default:
		select {
		case <-out:
		default:
		}
		select {
		case out <- state:
		default:
		}
	}
}

func startAudioPipeline(ctx context.Context, runtime *Runtime) (*ingest.Session, *recording.Manager, <-chan error, error) {
	audioErrs := make(chan error, 1)
	doc := runtime.Config()
	if doc == nil {
		return nil, nil, nil, errors.New("runtime config missing")
	}

	rec := recording.NewManager(recording.Config{
		OutputDir: runtime.RecordingsPath(),
		HangTime:  time.Duration(doc.Config.Recording.HangTimeSeconds) * time.Second,
		OnFinalized: func(meta recording.Metadata) error {
			entry := store.RecordingEntry{
				ID:        meta.ID,
				StartedAt: meta.StartedAt.Format(time.RFC3339Nano),
				EndedAt:   meta.EndedAt.Format(time.RFC3339Nano),
				Duration:  meta.Duration.String(),
				Frequency: meta.Frequency,
				System:    meta.System,
				Channel:   meta.Channel,
				Talkgroup: meta.Talkgroup,
				FilePath:  meta.FilePath,
				FileSize:  meta.FileSize,
				Trigger:   meta.Trigger,
			}
			if err := runtime.AppendRecording(entry); err != nil {
				return fmt.Errorf("persist recording metadata: %w", err)
			}
			log.Printf("recording finalized: %s (%s)", meta.FilePath, meta.Duration)
			return nil
		},
	})

	stateSub := runtime.SubscribeState(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case state, ok := <-stateSub:
				if !ok {
					return
				}
				prev := rec.Snapshot()
				if err := rec.UpdateTelemetry(state.Scanner, state.Scanner.UpdatedAt); err != nil {
					select {
					case audioErrs <- fmt.Errorf("recording telemetry update: %w", err):
					default:
					}
					return
				}
				next := rec.Snapshot()
				if runtime.state != nil && recordingStatusChanged(prev, next) {
					runtime.state.publish(runtime.StateSnapshot())
				}
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				prev := rec.Snapshot()
				if err := rec.Tick(t); err != nil {
					select {
					case audioErrs <- fmt.Errorf("recording tick: %w", err):
					default:
					}
					return
				}
				next := rec.Snapshot()
				if runtime.state != nil && recordingStatusChanged(prev, next) {
					runtime.state.publish(runtime.StateSnapshot())
				}
			}
		}
	}()

	audioSession, err := ingest.NewSession(ctx, ingest.Config{
		Address:           doc.Config.Scanner.IP,
		RTSPPort:          doc.Config.Scanner.RTSPPort,
		ReconnectDelay:    2 * time.Second,
		MaxReconnectFails: 5,
		OnFrame: func(frame ingest.Frame) {
			if err := rec.PushPCM(frame.Samples, frame.ReceivedAt); err != nil {
				select {
				case audioErrs <- fmt.Errorf("recording write: %w", err):
				default:
				}
			}
		},
	})
	if err != nil {
		return nil, nil, nil, err
	}

	go func() {
		select {
		case <-ctx.Done():
			return
		case err := <-audioSession.Fatal():
			if err == nil {
				return
			}
			select {
			case audioErrs <- err:
			default:
			}
		}
	}()

	return audioSession, rec, audioErrs, nil
}

func recordingStatusChanged(a, b recording.Status) bool {
	if a.Active != b.Active {
		return true
	}
	if a.Manual != b.Manual {
		return true
	}
	if a.Trigger != b.Trigger {
		return true
	}
	return !a.StartedAt.Equal(b.StartedAt)
}
