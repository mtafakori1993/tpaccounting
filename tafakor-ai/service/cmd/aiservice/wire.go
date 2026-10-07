package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
	"github.com/tafakorpardazan/tafakor-ai/internal/agent"
	"github.com/tafakorpardazan/tafakor-ai/internal/approval"
	"github.com/tafakorpardazan/tafakor-ai/internal/config"
	"github.com/tafakorpardazan/tafakor-ai/internal/files"
	"github.com/tafakorpardazan/tafakor-ai/internal/httpapi"
	"github.com/tafakorpardazan/tafakor-ai/internal/learning"
	"github.com/tafakorpardazan/tafakor-ai/internal/provider"
	"github.com/tafakorpardazan/tafakor-ai/internal/speech"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// wire attaches the feature layers to the HTTP server.
func wire(srv *httpapi.Server, cfg config.Config, st store.Repository, acc *accounting.Client, ai provider.AIProvider) {
	fs := &files.Store{Dir: cfg.DataDir, MaxBytes: cfg.MaxUploadBytes}
	know, _ := os.ReadFile(filepath.Join(cfg.KnowledgeDir, "accounting-model.md"))
	a := &agent.Agent{
		Store: st, Acc: acc, Router: provider.NewRouter(ai), VisionModel: cfg.VisionModel, Tools: tools.NewRegistry(), Files: fs,
		Approvals: &approval.Service{Store: st, TTL: cfg.ApprovalTTL}, Learn: &learning.Service{Store: st},
		Knowledge: string(know), EnvLockAuto: cfg.CompanyLockAuto,
	}
	var sp speech.SpeechProvider
	if cfg.SpeechEnabled {
		o, err := speech.NewOpenAICompatible(cfg.Speech.BaseURL, cfg.Speech.APIKey, cfg.Speech.Model, cfg.Speech.ProxyURL, cfg.NoProxy, cfg.Speech.Timeout)
		if err != nil {
			slog.Error("speech provider disabled", "err", err)
		} else {
			sp = o
		}
	}
	h := &agent.HTTP{A: a, Speech: sp, TTS: cfg.TTSEnabled, SpeechOn: sp != nil, TaskBudget: 5 * time.Minute}
	srv.Register(h.Register)
	go func() {
		for range time.Tick(24 * time.Hour) {
			fs.Purge(time.Now().Add(-cfg.AuditRetention))
		}
	}()
}
