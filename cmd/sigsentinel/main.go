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
		return gui.RuntimeState{
			Scanner: gui.ScannerStatus{
				Connected:     status.Connected,
				Mode:          status.Mode,
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
			},
		}
	}

	initialSettings := gui.Settings{}
	if cfg := runtime.Config(); cfg != nil {
		initialSettings = gui.Settings{
			ScannerIP:       cfg.Config.Scanner.IP,
			RecordingsPath:  cfg.Config.Storage.RecordingsPath,
			HangTimeSeconds: cfg.Config.Recording.HangTimeSeconds,
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
		EnqueueControl: func(intent gui.ControlIntent) {
			switch intent {
			case gui.IntentHoldCurrent:
				runtime.EnqueueControl(IntentHold)
			case gui.IntentResumeScan:
				runtime.EnqueueControl(IntentResumeScan)
			}
		},
		StartRecording: func() error {
			status := runtime.StateSnapshot().Scanner
			return recorder.StartManual(status, time.Now())
		},
		StopRecording: func() error {
			return recorder.StopManual(time.Now())
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
				if err := rec.UpdateTelemetry(state.Scanner, state.Scanner.UpdatedAt); err != nil {
					select {
					case audioErrs <- fmt.Errorf("recording telemetry update: %w", err):
					default:
					}
					return
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
				if err := rec.Tick(t); err != nil {
					select {
					case audioErrs <- fmt.Errorf("recording tick: %w", err):
					default:
					}
					return
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
