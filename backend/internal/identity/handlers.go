package identity

import (
	"net/http"
	"strings"

	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

const refreshCookieName = "gt_refresh"

// Handler exposes the identity service over HTTP.
type Handler struct {
	svc          *Service
	cookieSecure bool
	publicURL    string
}

// NewHandler returns the identity HTTP handler. publicURL is where browsers are sent after
// signing in with an external provider.
func NewHandler(svc *Service, cookieSecure bool, publicURL string) *Handler {
	return &Handler{svc: svc, cookieSecure: cookieSecure, publicURL: publicURL}
}

// isMobileClient reports whether the caller wants the refresh token in the response body
// (native apps) instead of an HttpOnly cookie (browsers).
func isMobileClient(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("X-Client"), "mobile")
}

// writeSession sets the refresh cookie for browsers and returns the session.
func (h *Handler) writeSession(w http.ResponseWriter, r *http.Request, status int, s *Session) error {
	if isMobileClient(r) {
		return httpx.JSON(w, status, s)
	}
	h.setRefreshCookie(w, s)
	body := *s
	body.RefreshToken = ""
	return httpx.JSON(w, status, body)
}

func (h *Handler) setRefreshCookie(w http.ResponseWriter, s *Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    s.RefreshToken,
		Path:     "/api/v1/auth",
		Expires:  s.RefreshTokenExpiresAt,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *Handler) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookieName, Value: "", Path: "/api/v1/auth", MaxAge: -1,
		HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteStrictMode,
	})
}

func refreshTokenFrom(r *http.Request, body string) string {
	if body != "" {
		return body
	}
	if c, err := r.Cookie(refreshCookieName); err == nil {
		return c.Value
	}
	return ""
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) error {
	var in RegisterInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	s, err := h.svc.Register(r.Context(), in)
	if err != nil {
		return err
	}
	return h.writeSession(w, r, http.StatusCreated, s)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	s, err := h.svc.Login(r.Context(), in.Email, in.Password)
	if err != nil {
		return err
	}
	return h.writeSession(w, r, http.StatusOK, s)
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			return err
		}
	}
	s, err := h.svc.Refresh(r.Context(), refreshTokenFrom(r, in.RefreshToken))
	if err != nil {
		h.clearCookie(w)
		return err
	}
	return h.writeSession(w, r, http.StatusOK, s)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			return err
		}
	}
	if err := h.svc.Logout(r.Context(), refreshTokenFrom(r, in.RefreshToken)); err != nil {
		return err
	}
	h.clearCookie(w)
	return httpx.NoContent(w)
}

func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email string `json:"email"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := h.svc.ForgotPassword(r.Context(), in.Email); err != nil {
		return err
	}
	return httpx.OK(w, map[string]string{"message": "If an account exists for that email, a reset link is on its way."})
}

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if err := h.svc.ResetPassword(r.Context(), in.Token, in.Password); err != nil {
		return err
	}
	return httpx.OK(w, map[string]string{"message": "Your password has been changed. You can now sign in."})
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.MustPrincipal(r)
	if err != nil {
		return err
	}
	me, err := h.svc.Me(r.Context(), p)
	if err != nil {
		return err
	}
	return httpx.OK(w, me)
}

func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.MustPrincipal(r)
	if err != nil {
		return err
	}
	var in UpdateProfileInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	profile, err := h.svc.UpdateProfile(r.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.OK(w, profile)
}

func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.MustPrincipal(r)
	if err != nil {
		return err
	}
	var in ChangePasswordInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	s, err := h.svc.ChangePassword(r.Context(), p, in)
	if err != nil {
		return err
	}
	return h.writeSession(w, r, http.StatusOK, s)
}

// ---- Administration -------------------------------------------------------------------

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) error {
	page := httpx.PageFrom(r)
	users, total, err := h.svc.ListUsers(r.Context(), UserFilter{
		Query:  httpx.QueryString(r, "q"),
		RoleID: httpx.QueryInt64(r, "role_id"),
		Status: httpx.QueryString(r, "status"),
	}, page)
	if err != nil {
		return err
	}
	return httpx.Paginated(w, users, total, page)
}

func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	u, err := h.svc.GetUser(r.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(w, u)
}

func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	var in UserInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	u, err := h.svc.CreateUser(r.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.Created(w, u)
}

func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in UserInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	u, err := h.svc.UpdateUser(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(w, u)
}

func (h *Handler) setStatus(active bool) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		p, _ := auth.MustPrincipal(r)
		id, err := httpx.PathID(r, "id")
		if err != nil {
			return err
		}
		u, err := h.svc.SetUserStatus(r.Context(), p, id, active)
		if err != nil {
			return err
		}
		return httpx.OK(w, u)
	}
}

// ActivateUser re-enables an account.
func (h *Handler) ActivateUser(w http.ResponseWriter, r *http.Request) error {
	return h.setStatus(true)(w, r)
}

// DeactivateUser disables an account and signs it out.
func (h *Handler) DeactivateUser(w http.ResponseWriter, r *http.Request) error {
	return h.setStatus(false)(w, r)
}

func (h *Handler) SetUserRoles(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		RoleIDs []int64 `json:"role_ids"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	u, err := h.svc.SetUserRoles(r.Context(), p, id, in.RoleIDs)
	if err != nil {
		return err
	}
	return httpx.OK(w, u)
}

func (h *Handler) SendPasswordReset(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.svc.SendPasswordReset(r.Context(), p, id); err != nil {
		return err
	}
	return httpx.OK(w, map[string]string{"message": "A password reset link has been emailed to the user."})
}

func (h *Handler) ListRoles(w http.ResponseWriter, r *http.Request) error {
	roles, err := h.svc.ListRoles(r.Context(), httpx.QueryString(r, "scope"))
	if err != nil {
		return err
	}
	return httpx.List(w, roles)
}

func (h *Handler) GetRole(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	role, err := h.svc.RoleByID(r.Context(), h.svc.db, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, role)
}

func (h *Handler) CreateRole(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	var in RoleInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	role, err := h.svc.CreateRole(r.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.Created(w, role)
}

func (h *Handler) UpdateRole(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in RoleInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	role, err := h.svc.UpdateRole(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(w, role)
}

func (h *Handler) DeleteRole(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.MustPrincipal(r)
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.svc.DeleteRole(r.Context(), p, id); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// Permissions returns the permission catalogue for the role editor.
func (h *Handler) Permissions(w http.ResponseWriter, r *http.Request) error {
	return httpx.List(w, rbac.Catalogue)
}
