// Package pdf is the single PDF engine of the application. Every PDF —
// invoices, receipts, statements, reports, letters — is described as a
// Document and rendered by Service.Render, which always wraps it in the
// company's official letterhead. There is no other way to produce a PDF.
package pdf

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

// Settings is the letterhead configuration, stored per company in app_settings
// under SettingsKey. Defaults reproduce the official A4 letterhead
// (Letterhead_Tafakor_Pardazan_A4_Color.pdf).
type Settings struct {
	Enabled  bool           `json:"enabled"`   // letterhead on PDF exports
	ForPrint bool           `json:"for_print"` // letterhead on documents opened for printing
	Header   HeaderSettings `json:"header"`
	Footer   FooterSettings `json:"footer"`
	Page     PageSettings   `json:"page"`
}

type HeaderSettings struct {
	Enabled      bool    `json:"enabled"`
	Mode         string  `json:"mode"`          // every | first
	HeightMM     float64 `json:"height_mm"`     // dark band height
	TopMarginMM  float64 `json:"top_margin_mm"` // space above logo and texts
	Title        string  `json:"title"`
	Subtitle     string  `json:"subtitle"`
	LogoSource   string  `json:"logo_source"` // letterhead | company
	LogoWidthMM  float64 `json:"logo_width_mm"`
	LogoPosition string  `json:"logo_position"` // right | left
	ShowFields   bool    `json:"show_fields"`   // number / date / attachment
}

type FooterSettings struct {
	Enabled      bool    `json:"enabled"`
	Text         string  `json:"text"` // items separated by " | ", supports {variables}
	Font         string  `json:"font"` // vazir | vazir-fa-digits
	SizePt       float64 `json:"size_pt"`
	Color        string  `json:"color"`
	ValueColor   string  `json:"value_color"`
	Align        string  `json:"align"` // right | center | left
	Separator    bool    `json:"separator"`
	HeightMM     float64 `json:"height_mm"` // dark band height (distance from page edge)
	OffsetMM     float64 `json:"offset_mm"` // text baseline lift inside the band
	PageNumbers  bool    `json:"page_numbers"`
	PageNumberTx string  `json:"page_number_text"`
}

type PageSettings struct {
	Size          string  `json:"size"`        // A4
	Orientation   string  `json:"orientation"` // portrait | landscape
	MarginTopMM   float64 `json:"margin_top_mm"`
	MarginBottom  float64 `json:"margin_bottom_mm"`
	MarginLeftMM  float64 `json:"margin_left_mm"`
	MarginRightMM float64 `json:"margin_right_mm"`
}

const SettingsKey = "letterhead"

// Defaults returns the configuration measured from the official letterhead.
func Defaults() Settings {
	return Settings{
		Enabled:  true,
		ForPrint: true,
		Header: HeaderSettings{
			Enabled: true, Mode: "every", HeightMM: 43.9, TopMarginMM: 9,
			Title:      "شرکت تفکر پردازان نوآوران فناوری دیجیتال ایرانیان",
			Subtitle:   "TAFAKOR PARDAZAN",
			LogoSource: "letterhead", LogoWidthMM: 30.5, LogoPosition: "right", ShowFields: true,
		},
		Footer: FooterSettings{
			Enabled: true,
			Text:    "شماره ثبت: ۸۲۱ | شناسه ملی: ۱۴۰۱۵۵۸۹۰۳۸",
			Font:    "vazir-fa-digits", SizePt: 8.5, Color: "#B7D8C5", ValueColor: "#FFFFFF",
			Align: "center", Separator: true, HeightMM: 17.1, OffsetMM: 0,
			PageNumbers: true, PageNumberTx: "صفحه {page_number} از {total_pages}",
		},
		Page: PageSettings{Size: "A4", Orientation: "portrait", MarginTopMM: 8, MarginBottom: 8, MarginLeftMM: 16, MarginRightMM: 16},
	}
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Normalize fills missing values and validates ranges.
func (s *Settings) Normalize() error {
	d := Defaults()
	v := apperr.NewValidator()
	in := func(f string, val, lo, hi float64) { v.Check(val >= lo && val <= hi, f, "invalid") }
	oneOf := func(f, val string, opts ...string) {
		ok := false
		for _, o := range opts {
			ok = ok || o == val
		}
		v.Check(ok, f, "invalid")
	}
	s.Header.Title, s.Header.Subtitle = strings.TrimSpace(s.Header.Title), strings.TrimSpace(s.Header.Subtitle)
	s.Footer.Text = strings.TrimSpace(s.Footer.Text)
	if s.Header.Mode == "" {
		s.Header.Mode = d.Header.Mode
	}
	if s.Header.LogoSource == "" {
		s.Header.LogoSource = d.Header.LogoSource
	}
	if s.Header.LogoPosition == "" {
		s.Header.LogoPosition = d.Header.LogoPosition
	}
	if s.Footer.Font == "" {
		s.Footer.Font = d.Footer.Font
	}
	if s.Footer.Align == "" {
		s.Footer.Align = d.Footer.Align
	}
	if s.Footer.ValueColor == "" {
		s.Footer.ValueColor = d.Footer.ValueColor
	}
	if s.Page.Size == "" {
		s.Page.Size = "A4"
	}
	if s.Page.Orientation == "" {
		s.Page.Orientation = "portrait"
	}
	oneOf("header.mode", s.Header.Mode, "every", "first")
	oneOf("header.logo_source", s.Header.LogoSource, "letterhead", "company")
	oneOf("header.logo_position", s.Header.LogoPosition, "right", "left")
	in("header.height_mm", s.Header.HeightMM, 20, 80)
	in("header.top_margin_mm", s.Header.TopMarginMM, 0, 30)
	in("header.logo_width_mm", s.Header.LogoWidthMM, 10, 70)
	v.MaxLen("header.title", s.Header.Title, 200)
	v.MaxLen("header.subtitle", s.Header.Subtitle, 100)
	oneOf("footer.font", s.Footer.Font, "vazir", "vazir-fa-digits")
	oneOf("footer.align", s.Footer.Align, "right", "center", "left")
	in("footer.size_pt", s.Footer.SizePt, 6, 16)
	in("footer.height_mm", s.Footer.HeightMM, 8, 40)
	in("footer.offset_mm", s.Footer.OffsetMM, -10, 10)
	v.Check(hexColor.MatchString(s.Footer.Color), "footer.color", "invalid")
	v.Check(hexColor.MatchString(s.Footer.ValueColor), "footer.value_color", "invalid")
	v.MaxLen("footer.text", s.Footer.Text, 500)
	v.MaxLen("footer.page_number_text", s.Footer.PageNumberTx, 100)
	oneOf("page.size", s.Page.Size, "A4")
	oneOf("page.orientation", s.Page.Orientation, "portrait", "landscape")
	in("page.margin_top_mm", s.Page.MarginTopMM, 0, 40)
	in("page.margin_bottom_mm", s.Page.MarginBottom, 0, 40)
	in("page.margin_left_mm", s.Page.MarginLeftMM, 5, 40)
	in("page.margin_right_mm", s.Page.MarginRightMM, 5, 40)
	return v.Err()
}

// LoadSettings returns the stored letterhead, or the defaults if none is stored yet.
func LoadSettings(ctx context.Context, q db.DBTX, companyID int64) (Settings, error) {
	s := Defaults()
	var raw []byte
	err := q.QueryRow(ctx, `SELECT value FROM app_settings WHERE company_id = $1 AND key = $2`, companyID, SettingsKey).Scan(&raw)
	if db.IsNoRows(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	// Unmarshal over the defaults so settings added in later versions get sensible values.
	if err := json.Unmarshal(raw, &s); err != nil {
		return Defaults(), nil
	}
	return s, nil
}

// SaveSettings validates and stores the letterhead.
func SaveSettings(ctx context.Context, q db.DBTX, companyID, userID int64, s *Settings) error {
	if err := s.Normalize(); err != nil {
		return err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO app_settings (company_id, key, value, updated_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (company_id, key) DO UPDATE SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		companyID, SettingsKey, raw, nullID(userID))
	return err
}

// EnsureDefaults stores the default letterhead for every company that has none,
// so a fresh installation starts with the official letterhead enabled.
func EnsureDefaults(ctx context.Context, q db.DBTX) error {
	raw, err := json.Marshal(Defaults())
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO app_settings (company_id, key, value)
		SELECT c.id, $1, $2 FROM companies c
		ON CONFLICT (company_id, key) DO NOTHING`, SettingsKey, raw)
	return err
}

func nullID(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	return &id
}
