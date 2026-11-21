package app

import (
	"net/http"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/ai"
	"github.com/kotswanemoeketsi/gotaticket/internal/audit"
	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/catalog"
	"github.com/kotswanemoeketsi/gotaticket/internal/checkin"
	"github.com/kotswanemoeketsi/gotaticket/internal/dashboard"
	"github.com/kotswanemoeketsi/gotaticket/internal/events"
	"github.com/kotswanemoeketsi/gotaticket/internal/identity"
	"github.com/kotswanemoeketsi/gotaticket/internal/organisers"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
	"github.com/kotswanemoeketsi/gotaticket/internal/reports"
	"github.com/kotswanemoeketsi/gotaticket/internal/sales"
)

// Handler builds the HTTP handler with every route of the API.
//
// Route-level middleware only checks that the caller holds a permission somewhere; the
// services then check it against the specific organiser that owns the data.
func (a *App) Handler() http.Handler {
	var (
		idh  = identity.NewHandler(a.Identity, a.Config.CookieSecure, a.Config.PublicURL)
		orh  = organisers.NewHandler(a.Organisers)
		cat  = catalog.NewHandler(a.DB, a.Now)
		evh  = events.NewHandler(a.Events, a.Storage)
		sh   = sales.NewHandler(a.Sales)
		ch   = checkin.NewHandler(a.Checkin)
		rh   = reports.NewHandler(a.Reports)
		dh   = dashboard.NewHandler(a.Dashboard)
		audh = audit.NewHandler(a.DB)
		aih  = ai.NewHandler(a.AI)
	)
	anyOf := auth.RequireAny
	platformOnly := auth.RequirePlatform

	r := httpx.NewRouter()
	api := r.Group("/api/v1")
	api.GET("/health", func(w http.ResponseWriter, req *http.Request) error {
		if err := a.DB.PingContext(req.Context()); err != nil {
			return err
		}
		return httpx.OK(w, map[string]string{"status": "ok", "payment_provider": a.Gateway.Name()})
	})

	// Authentication and the signed-in user's account.
	authLimit := a.Limiter.Middleware("auth", max(a.Config.AuthRateLimit, 1), time.Minute)
	api.POST("/auth/register", idh.Register, authLimit)
	api.POST("/auth/login", idh.Login, authLimit)
	api.POST("/auth/refresh", idh.Refresh)
	api.GET("/auth/providers", idh.Providers)
	api.GET("/auth/google/start", idh.GoogleStart, authLimit)
	api.GET("/auth/google/callback", idh.GoogleCallback, authLimit)
	api.POST("/auth/logout", idh.Logout)
	api.POST("/auth/forgot-password", idh.ForgotPassword, authLimit)
	api.POST("/auth/reset-password", idh.ResetPassword, authLimit)
	me := api.Group("", auth.RequireUser)
	me.GET("/auth/me", idh.Me)
	me.PUT("/auth/me", idh.UpdateProfile)
	me.POST("/auth/change-password", idh.ChangePassword)

	// Public marketplace.
	api.GET("/public/events", evh.ListPublic)
	api.GET("/public/events/{slug}", evh.GetPublic)
	api.GET("/public/categories", cat.ListCategories)
	api.GET("/public/locations", evh.PublicLocations)
	api.GET("/public/nearby", evh.PublicNearby)
	api.GET("/public/places", evh.PublicPlaces)
	api.GET("/public/features", aih.Features)
	api.GET("/public/search/interpret", aih.InterpretSearch)
	api.GET("/public/payment-options", sh.PaymentOptions)

	// Payment gateway webhooks (authenticated by signature, not by user).
	api.POST("/payments/webhooks/{provider}", sh.Webhook)

	// Customers.
	me.POST("/organisers/apply", orh.Apply)
	me.POST("/orders", sh.Reserve, a.Limiter.Middleware("reserve", 60, time.Minute))
	me.GET("/orders", sh.MyOrders)
	me.GET("/orders/{id}", sh.MyOrder)
	me.POST("/orders/{id}/checkout", sh.Checkout)
	me.POST("/orders/{id}/cancel", sh.CancelMyOrder)
	me.POST("/orders/{id}/verify-payment", sh.VerifyPayment)
	me.POST("/orders/{id}/refunds", sh.RequestRefund)
	me.GET("/orders/{id}/tickets.pdf", sh.OrderPDF)
	me.GET("/tickets", sh.MyTickets)
	me.PUT("/tickets/{id}/attendee", sh.UpdateAttendee)

	// Administration and organiser portal.
	adm := api.Group("/admin", auth.RequireUser)
	adm.GET("/dashboard", dh.Summary, anyOf(rbac.DashboardView))

	adm.GET("/events", evh.List, anyOf(rbac.EventsView))
	adm.POST("/events", evh.Create, anyOf(rbac.EventsCreate))
	adm.GET("/events/{id}", evh.Get, anyOf(rbac.EventsView))
	adm.PUT("/events/{id}", evh.Update, anyOf(rbac.EventsUpdate))
	adm.DELETE("/events/{id}", evh.Delete, anyOf(rbac.EventsDelete))
	adm.POST("/events/{id}/submit", evh.Lifecycle("submit"), anyOf(rbac.EventsUpdate))
	adm.POST("/events/{id}/withdraw", evh.Lifecycle("withdraw"), anyOf(rbac.EventsUpdate))
	adm.POST("/events/{id}/approve", evh.Lifecycle("approve"), anyOf(rbac.EventsReview))
	adm.POST("/events/{id}/reject", evh.Lifecycle("reject"), anyOf(rbac.EventsReview))
	adm.POST("/events/{id}/cancel", evh.Lifecycle("cancel"), anyOf(rbac.EventsCancel))
	adm.GET("/events/{id}/ticket-types", evh.ListTicketTypes, anyOf(rbac.EventsView))
	adm.POST("/events/{id}/ticket-types", evh.CreateTicketType, anyOf(rbac.TicketTypesManage))
	adm.PUT("/ticket-types/{id}", evh.UpdateTicketType, anyOf(rbac.TicketTypesManage))
	adm.DELETE("/ticket-types/{id}", evh.DeleteTicketType, anyOf(rbac.TicketTypesManage))
	adm.GET("/events/{id}/promotions", evh.ListPromotions, anyOf(rbac.PromotionsManage))
	adm.POST("/events/{id}/promotions", evh.CreatePromotion, anyOf(rbac.PromotionsManage))
	adm.PUT("/promotions/{id}", evh.UpdatePromotion, anyOf(rbac.PromotionsManage))
	adm.DELETE("/promotions/{id}", evh.DeletePromotion, anyOf(rbac.PromotionsManage))
	adm.POST("/events/{id}/complimentary", sh.IssueComplimentary, anyOf(rbac.TicketsComplimentary))
	adm.GET("/events/{id}/attendees", sh.EventAttendees, anyOf(rbac.AttendeesView))
	adm.GET("/events/{id}/checkins", ch.Overview, anyOf(rbac.CheckinScan, rbac.AttendeesView))
	adm.POST("/uploads", evh.UploadImage, anyOf(rbac.EventsCreate, rbac.EventsUpdate))
	adm.POST("/ai/event-draft", aih.DraftEvent, anyOf(rbac.EventsCreate, rbac.EventsUpdate))
	adm.GET("/ai/events/{id}/review", aih.LatestReview, anyOf(rbac.EventsReview))
	adm.POST("/ai/events/{id}/review", aih.ReviewEvent, anyOf(rbac.EventsReview))

	adm.GET("/checkin/events", ch.Events, anyOf(rbac.CheckinScan))
	adm.POST("/checkin/scan", ch.Scan, anyOf(rbac.CheckinScan))

	adm.GET("/orders", sh.ListOrders, anyOf(rbac.OrdersView))
	adm.GET("/orders/{id}", sh.GetOrder, anyOf(rbac.OrdersView))
	adm.GET("/orders/{id}/tickets.pdf", sh.OrderPDF, anyOf(rbac.OrdersView))
	adm.POST("/orders/{id}/resend", sh.ResendTickets, anyOf(rbac.OrdersManage))
	adm.POST("/orders/{id}/cancel", sh.CancelOrder, anyOf(rbac.OrdersManage))
	adm.POST("/orders/{id}/refunds", sh.CreateRefund, anyOf(rbac.RefundsManage))
	adm.PUT("/tickets/{id}/attendee", sh.UpdateAttendee, anyOf(rbac.OrdersManage))

	adm.GET("/payments", sh.ListPayments, anyOf(rbac.PaymentsView))
	adm.POST("/payments/{id}/confirm", sh.ConfirmEFT, anyOf(rbac.PaymentsConfirm))

	adm.GET("/refunds", sh.ListRefunds, anyOf(rbac.RefundsView))
	adm.POST("/refunds/{id}/approve", sh.ReviewRefund(true), anyOf(rbac.RefundsManage))
	adm.POST("/refunds/{id}/reject", sh.ReviewRefund(false), anyOf(rbac.RefundsManage))
	adm.POST("/refunds/{id}/process", sh.ProcessRefund, anyOf(rbac.RefundsProcess))

	adm.GET("/reports", rh.List, anyOf(rbac.ReportsView, rbac.ReportsFinancial))
	adm.GET("/reports/{name}", rh.Run, anyOf(rbac.ReportsView, rbac.ReportsFinancial))

	adm.POST("/categories", cat.CreateCategory, platformOnly(rbac.CategoriesManage))
	adm.PUT("/categories/{id}", cat.UpdateCategory, platformOnly(rbac.CategoriesManage))
	adm.DELETE("/categories/{id}", cat.DeleteCategory, platformOnly(rbac.CategoriesManage))
	adm.GET("/venues", cat.ListVenues, anyOf(rbac.EventsView, rbac.VenuesManage))
	adm.POST("/venues", cat.CreateVenue, anyOf(rbac.VenuesManage))
	adm.PUT("/venues/{id}", cat.UpdateVenue, anyOf(rbac.VenuesManage))
	adm.DELETE("/venues/{id}", cat.DeleteVenue, anyOf(rbac.VenuesManage))

	adm.GET("/organisers", orh.List, platformOnly(rbac.OrganisersView))
	adm.POST("/organisers", orh.Create, platformOnly(rbac.OrganisersManage))
	adm.GET("/organisers/{id}", orh.Get)
	adm.PUT("/organisers/{id}", orh.Update)
	adm.POST("/organisers/{id}/approve", orh.SetStatus(organisers.StatusActive), platformOnly(rbac.OrganisersManage))
	adm.POST("/organisers/{id}/suspend", orh.SetStatus(organisers.StatusSuspended), platformOnly(rbac.OrganisersManage))
	adm.POST("/organisers/{id}/reactivate", orh.SetStatus(organisers.StatusActive), platformOnly(rbac.OrganisersManage))
	adm.GET("/organisers/{id}/members", orh.Members)
	adm.POST("/organisers/{id}/members", orh.AddMember)
	adm.PUT("/organisers/{id}/members/{userID}", orh.UpdateMember)
	adm.DELETE("/organisers/{id}/members/{userID}", orh.RemoveMember)

	adm.GET("/users", idh.ListUsers, platformOnly(rbac.UsersView))
	adm.POST("/users", idh.CreateUser, platformOnly(rbac.UsersManage))
	adm.GET("/users/{id}", idh.GetUser, platformOnly(rbac.UsersView))
	adm.PUT("/users/{id}", idh.UpdateUser, platformOnly(rbac.UsersManage))
	adm.POST("/users/{id}/activate", idh.ActivateUser, platformOnly(rbac.UsersManage))
	adm.POST("/users/{id}/deactivate", idh.DeactivateUser, platformOnly(rbac.UsersManage))
	adm.PUT("/users/{id}/roles", idh.SetUserRoles, platformOnly(rbac.UsersManage))
	adm.POST("/users/{id}/password-reset", idh.SendPasswordReset, platformOnly(rbac.UsersManage))
	adm.GET("/roles", idh.ListRoles, anyOf(rbac.RolesManage, rbac.UsersManage, rbac.TeamManage, rbac.OrganisersManage))
	adm.GET("/roles/{id}", idh.GetRole, anyOf(rbac.RolesManage, rbac.UsersManage))
	adm.POST("/roles", idh.CreateRole, platformOnly(rbac.RolesManage))
	adm.PUT("/roles/{id}", idh.UpdateRole, platformOnly(rbac.RolesManage))
	adm.DELETE("/roles/{id}", idh.DeleteRole, platformOnly(rbac.RolesManage))
	adm.GET("/permissions", idh.Permissions, anyOf(rbac.RolesManage))

	adm.GET("/audit-logs", audh.List, anyOf(rbac.AuditView))

	// Uploaded images and, in development, the sandbox payment page.
	r.Mount("/uploads/", a.Storage.Handler())
	if a.Sandbox != nil {
		r.Handle(http.MethodGet, "/sandbox/checkout/{reference}", http.HandlerFunc(a.Sandbox.CheckoutPage))
		r.Handle(http.MethodPost, "/sandbox/checkout/{reference}", http.HandlerFunc(a.Sandbox.CheckoutPage))
	}

	// The website itself, when this process serves it (the Docker image does).
	if a.web != nil {
		r.Mount("/", a.web)
	}

	authn := auth.NewAuthenticator(a.Tokens, a.Loader)
	return httpx.Chain(r,
		httpx.RequestID,
		httpx.Logger(a.Logger),
		httpx.Recoverer,
		httpx.SecurityHeaders,
		httpx.ClientInfo(a.Config.TrustProxyHeaders),
		httpx.CORS(a.Config.CORSAllowedOrigins),
		authn.Middleware,
	)
}
