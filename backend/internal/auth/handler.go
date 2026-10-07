package auth

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
)

// PublicRoutes are reachable without a session.
func (s *Service) PublicRoutes(r chi.Router) {
	r.Post("/login", httpx.Handler(s.login))
	r.Post("/logout", httpx.Handler(s.logout))
}

// Routes require an authenticated session.
func (s *Service) Routes(r chi.Router) {
	r.Get("/me", httpx.Handler(s.me))
	r.Put("/preferences", httpx.Handler(s.preferences))
	r.Post("/change-password", httpx.Handler(s.changePassword))
}

type loginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) error {
	ip := identity.MetaFrom(r.Context()).IP
	if !s.limiter.Allow("login:" + ip) {
		return apperr.New(http.StatusTooManyRequests, "rate_limited", "too many login attempts, try again later")
	}
	var in loginInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	v := apperr.NewValidator()
	v.Required("email", in.Email)
	v.Required("password", in.Password)
	if err := v.Err(); err != nil {
		return err
	}
	var (
		userID, companyID int64
		hash              string
		active            bool
	)
	err := s.Pool.QueryRow(r.Context(), `SELECT id, company_id, password_hash, is_active FROM users WHERE lower(email) = $1`, in.Email).
		Scan(&userID, &companyID, &hash, &active)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	if err == pgx.ErrNoRows {
		// Spend comparable time so response timing does not reveal valid emails.
		CheckPassword(dummyHash(), in.Password)
		return apperr.New(http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
	}
	if !CheckPassword(hash, in.Password) {
		ctx := identity.WithPrincipal(r.Context(), &identity.Principal{UserID: userID, CompanyID: companyID})
		_ = audit.Record(ctx, s.Pool, "login_failed", "user", userID, nil, nil)
		return apperr.New(http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
	}
	if !active {
		return apperr.New(http.StatusUnauthorized, "user_inactive", "this user is disabled")
	}
	err = db.WithTx(r.Context(), s.Pool, func(tx pgx.Tx) error {
		if err := s.StartSession(r.Context(), tx, w, userID, companyID); err != nil {
			return err
		}
		ctx := identity.WithPrincipal(r.Context(), &identity.Principal{UserID: userID, CompanyID: companyID})
		return audit.Record(ctx, tx, "login", "user", userID, nil, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		if _, err := s.Pool.Exec(r.Context(), `UPDATE sessions SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`, s.hashToken(c.Value)); err != nil {
			return err
		}
	}
	s.clearCookies(w)
	httpx.NoContent(w)
	return nil
}

type CompanyInfo struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	NameEn         string `json:"name_en"`
	HasLogo        bool   `json:"has_logo"`
	BaseCurrency   string `json:"base_currency"`
	AmountDecimals int    `json:"amount_decimals"`
	Calendar       string `json:"calendar"`
}

type FiscalYearInfo struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	StartDate time.Time `json:"start_date"`
	EndDate   time.Time `json:"end_date"`
	Status    string    `json:"status"`
}

type Me struct {
	ID          int64           `json:"id"`
	Email       string          `json:"email"`
	FullName    string          `json:"full_name"`
	Role        string          `json:"role"`
	Language    string          `json:"language"`
	Theme       string          `json:"theme"`
	Permissions []string        `json:"permissions"`
	Company     CompanyInfo     `json:"company"`
	FiscalYear  *FiscalYearInfo `json:"fiscal_year"`
}

func (s *Service) me(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	m := Me{ID: p.UserID, Email: p.Email, FullName: p.FullName, Role: p.RoleCode}
	for k := range p.Perms {
		m.Permissions = append(m.Permissions, k)
	}
	sort.Strings(m.Permissions)
	err := s.Pool.QueryRow(r.Context(), `
		SELECT u.language, u.theme, c.id, c.name, c.name_en, c.logo_key IS NOT NULL, c.base_currency, c.amount_decimals, c.calendar
		FROM users u JOIN companies c ON c.id = $2 WHERE u.id = $1`, p.UserID, p.CompanyID).
		Scan(&m.Language, &m.Theme, &m.Company.ID, &m.Company.Name, &m.Company.NameEn, &m.Company.HasLogo,
			&m.Company.BaseCurrency, &m.Company.AmountDecimals, &m.Company.Calendar)
	if err != nil {
		return err
	}
	var fy FiscalYearInfo
	err = s.Pool.QueryRow(r.Context(), `
		SELECT id, name, start_date, end_date, status FROM fiscal_years
		WHERE company_id = $1 ORDER BY (CURRENT_DATE BETWEEN start_date AND end_date) DESC, start_date DESC LIMIT 1`, p.CompanyID).
		Scan(&fy.ID, &fy.Name, &fy.StartDate, &fy.EndDate, &fy.Status)
	if err == nil {
		m.FiscalYear = &fy
	} else if err != pgx.ErrNoRows {
		return err
	}
	httpx.OK(w, m)
	return nil
}

type prefsInput struct {
	Language string `json:"language"`
	Theme    string `json:"theme"`
}

func (s *Service) preferences(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in prefsInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	v := apperr.NewValidator()
	v.Check(in.Language == "" || in.Language == "fa" || in.Language == "en", "language", "invalid")
	v.Check(in.Theme == "" || in.Theme == "light" || in.Theme == "dark", "theme", "invalid")
	if err := v.Err(); err != nil {
		return err
	}
	if _, err := s.Pool.Exec(r.Context(), `
		UPDATE users SET language = COALESCE(NULLIF($2, ''), language), theme = COALESCE(NULLIF($3, ''), theme), updated_at = now()
		WHERE id = $1`, p.UserID, in.Language, in.Theme); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

type changePasswordInput struct {
	Current string `json:"current_password"`
	New     string `json:"new_password"`
}

func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	if !s.limiter.Allow("pw:" + identity.MetaFrom(r.Context()).IP) {
		return apperr.New(http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
	}
	var in changePasswordInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	v := apperr.NewValidator()
	v.Required("current_password", in.Current)
	ValidatePassword(v, "new_password", in.New)
	if err := v.Err(); err != nil {
		return err
	}
	var hash string
	if err := s.Pool.QueryRow(r.Context(), `SELECT password_hash FROM users WHERE id = $1`, p.UserID).Scan(&hash); err != nil {
		return err
	}
	if !CheckPassword(hash, in.Current) {
		v := apperr.NewValidator()
		v.Check(false, "current_password", "wrong_password")
		return v.Err()
	}
	newHash, err := HashPassword(in.New)
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, p.UserID, newHash); err != nil {
			return err
		}
		// Sign out every other session of this user.
		if _, err := tx.Exec(r.Context(), `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND id <> $2 AND revoked_at IS NULL`, p.UserID, p.SessionID); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "change_password", "user", p.UserID, nil, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
