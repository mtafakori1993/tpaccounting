package pdf

import (
	"fmt"
	"os"
	"path/filepath"
)

// installFonts writes the embedded Vazir fonts and a fontconfig file into a
// private directory and returns the fontconfig path for Chromium. This makes
// the fonts available to page header/footer templates, which cannot load web
// fonts, and guarantees TrueType embedding independent of the host's fonts.
func installFonts() (string, error) {
	dir, err := os.MkdirTemp("", "tp-pdf-fonts-")
	if err != nil {
		return "", err
	}
	entries, err := assets.ReadDir("assets")
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".ttf" {
			continue
		}
		b, err := assets.ReadFile("assets/" + e.Name())
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			return "", err
		}
	}
	conf := fmt.Sprintf(`<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "fonts.dtd">
<fontconfig>
  <dir>%s</dir>
  <cachedir>%s</cachedir>
  <include ignore_missing="yes">/etc/fonts/fonts.conf</include>
</fontconfig>
`, dir, filepath.Join(dir, "cache"))
	path := filepath.Join(dir, "fonts.conf")
	return path, os.WriteFile(path, []byte(conf), 0o644)
}
