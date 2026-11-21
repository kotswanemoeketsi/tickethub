package identity

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
)

const googleStateCookie = "gt_oauth_state"

// Providers tells the sign-in page which external sign-in buttons to show.
func (h *Handler) Providers(w http.ResponseWriter, _ *http.Request) error {
	return httpx.OK(w, map[string]bool{"google": h.svc.GoogleEnabled()})
}

// GoogleStart sends the browser to Google's account chooser.
func (h *Handler) GoogleStart(w http.ResponseWriter, r *http.Request) error {
	redirect, state, err := h.svc.StartGoogle(r.Context(), r.URL.Query().Get("next"))
	if err != nil {
		return err
	}
	// SameSite=Lax so the cookie comes back on Google's top-level redirect to the callback.
	http.SetCookie(w, &http.Cookie{
		Name: googleStateCookie, Value: state, Path: "/api/v1/auth/google", MaxAge: int(googleStateTTL.Seconds()),
		HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, redirect, http.StatusFound)
	return nil
}

// GoogleCallback finishes the sign-in, sets the session cookie and returns the browser to
// the app. Failures go back to the sign-in page with a short error code.
func (h *Handler) GoogleCallback(w http.ResponseWriter, r *http.Request) error {
	http.SetCookie(w, &http.Cookie{
		Name: googleStateCookie, Value: "", Path: "/api/v1/auth/google", MaxAge: -1,
		HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode,
	})
	q := r.URL.Query()
	if q.Get("error") != "" { // the person cancelled or Google refused
		h.redirectToLogin(w, r, "cancelled", "/")
		return nil
	}
	var cookieState string
	if c, err := r.Cookie(googleStateCookie); err == nil {
		cookieState = c.Value
	}
	session, next, err := h.svc.FinishGoogle(r.Context(), q.Get("state"), cookieState, q.Get("code"))
	if err != nil {
		var sso *ErrSSO
		if !errors.As(err, &sso) {
			return err
		}
		h.redirectToLogin(w, r, sso.Code, next)
		return nil
	}
	h.setRefreshCookie(w, session)
	http.Redirect(w, r, h.publicURL+"/auth/complete?next="+url.QueryEscape(next), http.StatusFound)
	return nil
}

func (h *Handler) redirectToLogin(w http.ResponseWriter, r *http.Request, code, next string) {
	v := url.Values{"sso_error": {code}}
	if next != "/" {
		v.Set("next", next)
	}
	http.Redirect(w, r, h.publicURL+"/login?"+v.Encode(), http.StatusFound)
}
