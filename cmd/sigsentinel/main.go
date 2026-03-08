package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jentfoo/SignalSentinel/internal/audio/ingest"
	"github.com/jentfoo/SignalSentinel/internal/audio/recording"
	"github.com/jentfoo/SignalSentinel/internal/store"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "path to config YAML")
	flag.Parse()

	logger := log.New(os.Stderr, "", log.LstdFlags|log.LUTC)
	if err := run(configPath, logger); err != nil {
		logger.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

func run(configPath string, logger *log.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runtime, err := StartRuntime(ctx, Options{
		ConfigPath: configPath,
		Logger:     logger,
	})
	if err != nil {
		return fmt.Errorf("startup: %w", err)
	}
	defer func() { _ = runtime.Close() }()

	audioSession, recorder, audioErrs, err := startAudioPipeline(ctx, runtime, logger)
	if err != nil {
		return fmt.Errorf("startup: %w", err)
	}
	defer func() {
		if closeErr := recorder.Close(); closeErr != nil {
			logger.Printf("recorder close error: %v", closeErr)
		}
	}()
	defer func() { _ = audioSession.Close() }()

	return waitForExit(ctx, runtime, audioErrs)
}

func waitForExit(ctx context.Context, runtime *Runtime, audioErrs <-chan error) error {
	runtimeDone := make(chan error, 1)
	go func() {
		runtimeDone <- runtime.Wait()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-runtimeDone:
			return err
		case err := <-audioErrs:
			if err == nil || errors.Is(err, context.Canceled) {
				continue
			}
			return err
		}
	}
}

func startAudioPipeline(ctx context.Context, runtime *Runtime, logger *log.Logger) (*ingest.Session, *recording.Manager, <-chan error, error) {
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
			if err := runtime.Store().AppendRecording(entry); err != nil {
				return fmt.Errorf("persist recording metadata: %w", err)
			}
			logger.Printf("recording finalized: %s (%s)", meta.FilePath, meta.Duration)
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
		Logger:            logger,
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
