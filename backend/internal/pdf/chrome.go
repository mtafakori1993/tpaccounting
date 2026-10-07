package pdf

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Renderer prints HTML to PDF with a headless Chromium — the same engine the
// official letterhead was produced with, so Persian shaping, embedded fonts
// and A4 geometry match it exactly.
type Renderer struct {
	execPath string
	mu       sync.Mutex
	browser  context.Context
	cancel   context.CancelFunc
	sem      chan struct{}
}

// ErrUnavailable means no Chromium binary is installed.
var ErrUnavailable = errors.New("pdf renderer unavailable: chromium not found")

// FindChrome locates a Chromium binary (CHROME_PATH wins).
func FindChrome() string {
	if p := os.Getenv("CHROME_PATH"); p != "" {
		return p
	}
	for _, name := range []string{"chromium-browser", "chromium", "google-chrome-stable", "google-chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

func NewRenderer(execPath string) *Renderer {
	return &Renderer{execPath: execPath, sem: make(chan struct{}, 3)}
}

func (r *Renderer) browserCtx() (context.Context, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.execPath == "" {
		return nil, ErrUnavailable
	}
	if r.browser != nil && r.browser.Err() == nil {
		return r.browser, nil
	}
	fontsConf, err := installFonts()
	if err != nil {
		return nil, fmt.Errorf("install pdf fonts: %w", err)
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Env("FONTCONFIG_FILE="+fontsConf),
		chromedp.ExecPath(r.execPath),
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("font-render-hinting", "none"),
		chromedp.Flag("disable-extensions", true),
	)
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancelCtx := chromedp.NewContext(alloc)
	if err := chromedp.Run(ctx); err != nil {
		cancelCtx()
		cancelAlloc()
		return nil, fmt.Errorf("start chromium: %w", err)
	}
	r.browser = ctx
	r.cancel = func() { cancelCtx(); cancelAlloc() }
	slog.Info("pdf renderer started", "chrome", r.execPath)
	return ctx, nil
}

// Close stops the browser.
func (r *Renderer) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
		r.browser = nil
	}
}

type printJob struct {
	HTML                    string
	Header, Footer          string
	WidthMM, HeightMM       float64
	MarginTop, MarginBottom float64
	MarginLeft, MarginRight float64
}

const inch = 25.4

func (r *Renderer) Print(ctx context.Context, job printJob) ([]byte, error) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	browser, err := r.browserCtx()
	if err != nil {
		return nil, err
	}
	tab, cancelTab := chromedp.NewContext(browser)
	defer cancelTab()
	tab, cancelTimeout := context.WithTimeout(tab, 90*time.Second)
	defer cancelTimeout()
	stopOnCancel := context.AfterFunc(ctx, cancelTab)
	defer stopOnCancel()

	chromedp.ListenTarget(tab, func(ev any) {
		if e, ok := ev.(*fetch.EventRequestPaused); ok {
			go func() {
				_ = chromedp.Run(tab, fetch.FailRequest(e.RequestID, network.ErrorReasonBlockedByClient))
			}()
		}
	})
	var out []byte
	err = chromedp.Run(tab,
		// Documents are self-contained (data: URIs only): every network request
		// is intercepted and refused, so rendering can never reach the network.
		fetch.Enable(),
		chromedp.Navigate("about:blank"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			tree, err := page.GetFrameTree().Do(ctx)
			if err != nil {
				return err
			}
			return page.SetDocumentContent(tree.Frame.ID, job.HTML).Do(ctx)
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, exc, err := runtime.Evaluate(`document.fonts.ready.then(() => true)`).WithAwaitPromise(true).Do(ctx)
			if err == nil && exc != nil {
				return fmt.Errorf("font loading failed: %s", exc.Text)
			}
			return err
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			p := page.PrintToPDF().
				WithPaperWidth(job.WidthMM / inch).WithPaperHeight(job.HeightMM / inch).
				WithMarginTop(job.MarginTop / inch).WithMarginBottom(job.MarginBottom / inch).
				WithMarginLeft(job.MarginLeft / inch).WithMarginRight(job.MarginRight / inch).
				WithPrintBackground(true).
				WithDisplayHeaderFooter(true).
				WithHeaderTemplate(job.Header).WithFooterTemplate(job.Footer).
				WithPreferCSSPageSize(false)
			var err error
			out, _, err = p.Do(ctx)
			return err
		}),
	)
	if err != nil {
		if browser.Err() != nil {
			r.Close() // restart on the next request
		}
		return nil, err
	}
	return out, nil
}
