// Package seed loads a realistic demo dataset by driving the real services — reservations,
// payments through the sandbox gateway, EFT confirmations, check-ins and refunds — while
// moving a simulated clock through the last six weeks. The resulting data is therefore
// exactly what the platform itself would have produced.
package seed

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/app"
	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/catalog"
	"github.com/kotswanemoeketsi/gotaticket/internal/checkin"
	"github.com/kotswanemoeketsi/gotaticket/internal/events"
	"github.com/kotswanemoeketsi/gotaticket/internal/identity"
	"github.com/kotswanemoeketsi/gotaticket/internal/organisers"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/httpx"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
	"github.com/kotswanemoeketsi/gotaticket/internal/sales"
)

// DemoPassword is the password of every demo account.
const DemoPassword = "Password123"

type seeder struct {
	ctx   context.Context
	a     *app.App
	clock *clock.Fake
	real  time.Time
	loc   *time.Location
	users map[string]*auth.Principal
	hash  string

	categoryNames map[int64]string // for choosing each event's artwork style

	createdSuperAdmin bool
}

// Run seeds an empty database. clk must be the fake clock the app was built with.
func Run(ctx context.Context, a *app.App, clk *clock.Fake) error {
	var events int
	if err := a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&events); err != nil {
		return err
	}
	if events > 0 {
		a.Logger.Info("database already contains events; skipping demo data")
		return nil
	}
	loc, err := time.LoadLocation(a.Config.DefaultTimezone)
	if err != nil {
		return err
	}
	hash, err := auth.PasswordHasher{Iterations: a.Config.PasswordIterations}.Hash(DemoPassword)
	if err != nil {
		return err
	}
	s := &seeder{ctx: ctx, a: a, clock: clk, real: clk.Now(), loc: loc, users: map[string]*auth.Principal{}, hash: hash}
	if err := s.run(); err != nil {
		return err
	}
	clk.Set(s.real)
	// Treat emails for the simulated past as already sent, so the first worker run does not
	// flood the outbox; recent ones (such as the pending EFT instructions) are still delivered.
	if _, err := a.DB.ExecContext(ctx, `UPDATE jobs SET status = 'completed', updated_at = ? WHERE status = 'pending' AND created_at < ?`,
		s.real, s.real.Add(-6*time.Hour)); err != nil {
		return err
	}
	s.printSummary()
	return nil
}

// at moves the simulated clock to days (and hours) relative to the real start time.
func (s *seeder) at(days int, hours float64) {
	s.clock.Set(s.real.Add(time.Duration(days)*24*time.Hour + time.Duration(hours*float64(time.Hour))))
}

// local formats a wall-clock time days from now at hh:mm in the platform time zone.
func (s *seeder) local(days, hour, minute int) string {
	d := s.real.In(s.loc).AddDate(0, 0, days)
	return time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, s.loc).Format("2006-01-02T15:04")
}

func (s *seeder) principal(email string) *auth.Principal {
	if p, ok := s.users[email]; ok {
		return p
	}
	var id int64
	if err := s.a.DB.QueryRowContext(s.ctx, `SELECT id FROM users WHERE email = ?`, email).Scan(&id); err != nil {
		panic(fmt.Sprintf("seed: unknown user %s: %v", email, err))
	}
	s.a.Loader.Invalidate(s.ctx, id)
	p, err := s.a.Loader.Load(s.ctx, id)
	if err != nil {
		panic(fmt.Sprintf("seed: load principal %s: %v", email, err))
	}
	s.users[email] = p
	return p
}

// refresh reloads a principal after its roles or memberships changed.
func (s *seeder) refresh(email string) *auth.Principal {
	delete(s.users, email)
	return s.principal(email)
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func (s *seeder) run() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("seed failed: %v", r)
		}
	}()
	ctx := s.ctx
	s.at(-42, 0)

	// --- People --------------------------------------------------------------------------
	staff := []struct{ email, first, last, role string }{
		{"admin@gotaticket.test", "Naledi", "Dlamini", rbac.RoleAdmin},
		{"finance@gotaticket.test", "Pieter", "van Wyk", rbac.RoleFinanceManager},
		{"ticketing@gotaticket.test", "Ayesha", "Patel", rbac.RoleTicketingOfficer},
		{"checkin@gotaticket.test", "Sipho", "Ndlovu", rbac.RoleCheckinOfficer},
		{"organiser@gotaticket.test", "Lerato", "Mokoena", rbac.RoleCustomer},
		{"organiser2@gotaticket.test", "James", "Botha", rbac.RoleCustomer},
		{"doorstaff@gotaticket.test", "Thabo", "Khumalo", rbac.RoleCustomer},
		{"marketing@gotaticket.test", "Zanele", "Mthembu", rbac.RoleCustomer},
	}
	for _, u := range staff {
		s.createUser(u.email, u.first, u.last, u.role)
	}
	if s.superAdminCount() == 0 {
		s.createUser("superadmin@gotaticket.test", "Super", "Administrator", rbac.RoleSuperAdmin)
		s.createdSuperAdmin = true
	}
	firstNames := []string{"Ayanda", "Bongani", "Chloe", "Dineo", "Ethan", "Fatima", "Gugu", "Hannah", "Itumeleng", "Jabu", "Kayla",
		"Lindiwe", "Mandla", "Nomsa", "Olwethu", "Priya", "Refilwe", "Sizwe", "Tamsin", "Unathi", "Vusi", "Wandile", "Xolani", "Yolanda",
		"Zinhle", "Aiden", "Busi", "Carmen", "Dumisani", "Emma"}
	lastNames := []string{"Nkosi", "Smith", "Naidoo", "Van der Merwe", "Mahlangu", "Jacobs", "Pillay", "Molefe", "Fourie", "Zulu"}
	var customers []string
	s.createUser("customer@gotaticket.test", "Kagiso", "Sithole", rbac.RoleCustomer)
	customers = append(customers, "customer@gotaticket.test")
	for i, f := range firstNames {
		email := fmt.Sprintf("%s.%d@example.com", strings.ToLower(f), i+1)
		s.createUser(email, f, lastNames[i%len(lastNames)], rbac.RoleCustomer)
		customers = append(customers, email)
	}
	admin := s.principal("admin@gotaticket.test")

	// A custom platform role, as an administrator would configure it.
	marketing := must(s.a.Identity.CreateRole(ctx, admin, identity.RoleInput{
		Name: "Marketing Manager", Description: "Sees events, sales reports and the dashboard across all organisers",
		Scope: rbac.ScopePlatform, Permissions: []string{rbac.DashboardView, rbac.EventsView, rbac.ReportsView, rbac.ReportsExport, rbac.AllOrganisers},
	}))
	must(s.a.Identity.SetUserRoles(ctx, admin, s.principal("marketing@gotaticket.test").UserID, []int64{marketing.ID}))

	// --- Organisers ----------------------------------------------------------------------
	orgA := must(s.a.Organisers.Create(ctx, admin, organisers.Input{
		Name: "Soweto Sound Productions", Email: "hello@sowetosound.example", Phone: "+27 11 555 0101",
		Description: "Live music, festivals and late-night sessions across Gauteng.", OwnerEmail: "organiser@gotaticket.test",
	}))
	orgB := must(s.a.Organisers.Create(ctx, admin, organisers.Input{
		Name: "Cape Town Comedy Collective", Email: "bookings@ctcomedy.example", Phone: "+27 21 555 0199",
		Description: "Stand-up, conferences and markets in the Western Cape.", OwnerEmail: "organiser2@gotaticket.test",
	}))
	checkinRole := must(s.a.Identity.RoleIDBySlug(ctx, s.a.DB, rbac.RoleOrganiserCheckin))
	must(s.a.Organisers.AddMember(ctx, s.refresh("organiser@gotaticket.test"), orgA.ID, organisers.MemberInput{
		Email: "doorstaff@gotaticket.test", RoleID: checkinRole,
	}))
	orgAUser, orgBUser := s.refresh("organiser@gotaticket.test"), s.refresh("organiser2@gotaticket.test")
	// A pending application waiting for approval.
	s.createUser("newpromoter@gotaticket.test", "Mpho", "Radebe", rbac.RoleCustomer)
	must(s.a.Organisers.Apply(ctx, s.principal("newpromoter@gotaticket.test"), organisers.Input{
		Name: "Durban Beats Events", Email: "info@durbanbeats.example", Description: "Beach parties and gqom nights in Durban.",
	}))

	// --- Catalogue -----------------------------------------------------------------------
	categories := map[string]int64{}
	s.categoryNames = map[int64]string{}
	for _, name := range []string{"Music", "Festivals", "Comedy", "Theatre", "Sport", "Business & Tech", "Food & Drink", "Arts & Culture"} {
		var id int64
		res, err := s.a.DB.ExecContext(ctx, `INSERT INTO event_categories (name, slug, description, created_at, updated_at) VALUES (?, ?, '', ?, ?)`,
			name, slugify(name), s.clock.Now(), s.clock.Now())
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		categories[name] = id
		s.categoryNames[id] = name
	}
	venues := map[string]int64{}
	for _, v := range []struct{ name, address, city, province string }{
		{"Jozi Dome Arena", "Corner Hans Pirow & Northern Pkwy", "Johannesburg", "Gauteng"},
		{"Orlando Amphitheatre", "Mooki St, Orlando East", "Soweto", "Gauteng"},
		{"Mother City Theatre", "18 Long St", "Cape Town", "Western Cape"},
		{"Harbour Convention Centre", "1 Lower Long St", "Cape Town", "Western Cape"},
		{"Umhlanga Waterfront Pavilion", "Lagoon Dr", "Durban", "KwaZulu-Natal"},
		{"Karoo Sky Lodge", "R63", "Sutherland", "Northern Cape"},
		{"Highveld Stadium", "Kirkness St, Arcadia", "Pretoria", "Gauteng"},
		{"Bay Playhouse", "12 Govan Mbeki Ave", "Gqeberha", "Eastern Cape"},
		{"Rose City Hall", "Charles St", "Bloemfontein", "Free State"},
		{"Florida Road Courtyard", "275 Florida Rd", "Durban", "KwaZulu-Natal"},
	} {
		res, err := s.a.DB.ExecContext(ctx, `INSERT INTO venues (organiser_id, name, address_line, city, province, country, created_at, updated_at)
			VALUES (NULL, ?, ?, ?, ?, 'South Africa', ?, ?)`, v.name, v.address, v.city, v.province, s.clock.Now(), s.clock.Now())
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		venues[v.name] = id
	}
	if _, err := catalog.BackfillVenueLocations(ctx, s.a.DB, s.clock.Now()); err != nil {
		return err
	}

	// --- Events --------------------------------------------------------------------------
	type tt = events.TicketTypeInput
	spring := s.event(orgAUser, admin, events.Input{
		OrganiserID: orgA.ID, CategoryID: categories["Music"], VenueID: venues["Jozi Dome Arena"], Title: "Spring Classics at the Dome",
		Summary: "An evening of orchestral favourites under the Dome lights.", Description: "The Johannesburg Festival Orchestra performs well-loved classics, from Vivaldi's Spring to Grieg's Morning Mood.\n\nDoors open an hour before the performance.",
		StartsAt: s.local(-12, 19, 0), EndsAt: s.local(-12, 22, 0), Capacity: 400, RefundsAllowed: true, RefundCutoffHours: 48,
	}, []tt{{Name: "General", Category: "general", PriceCents: 22000, QuantityTotal: 300, MaxPerOrder: 8}, {Name: "Premium", Category: "vip", PriceCents: 45000, QuantityTotal: 100, MaxPerOrder: 6}}, true)

	jazz := s.event(orgAUser, admin, events.Input{
		OrganiserID: orgA.ID, CategoryID: categories["Music"], VenueID: venues["Orlando Amphitheatre"], Title: "Jozi Jazz Under the Stars",
		Summary: "South Africa's finest jazz musicians, one unforgettable night.", Description: "Bring a blanket and settle in for an open-air night of jazz featuring a line-up of local legends and rising stars.\n\nFood trucks and a cash bar on site. No under-16s.",
		StartsAt: s.local(20, 18, 0), EndsAt: s.local(20, 23, 30), Capacity: 680, RefundsAllowed: true, RefundCutoffHours: 72,
	}, []tt{
		{Name: "Early Bird", Category: "early_bird", PriceCents: 25000, QuantityTotal: 100, MaxPerOrder: 4, SalesEndAt: s.local(-5, 23, 59)},
		{Name: "General Admission", Category: "general", PriceCents: 35000, QuantityTotal: 500, MaxPerOrder: 10},
		{Name: "VIP Lounge", Category: "vip", PriceCents: 95000, QuantityTotal: 80, MaxPerOrder: 4, Description: "Reserved seating, welcome drink and lounge access"},
	}, true)
	must(s.a.Events.CreatePromotion(ctx, orgAUser, jazz.ID, events.PromotionInput{Code: "JAZZ15", Description: "15% off for newsletter subscribers",
		DiscountType: "percentage", DiscountValue: 1500, MaxUses: intPtr(100), IsActive: true}))

	amapiano := s.event(orgAUser, admin, events.Input{
		OrganiserID: orgA.ID, CategoryID: categories["Festivals"], VenueID: venues["Orlando Amphitheatre"], Title: "Soweto Amapiano Festival",
		Summary: "Twelve hours of the biggest amapiano DJs.", Description: "The biggest names in amapiano take over Orlando for a full day of music, food and culture.",
		StartsAt: s.local(35, 12, 0), EndsAt: s.local(35, 23, 59), Capacity: 2300, RefundsAllowed: false,
	}, []tt{{Name: "General", Category: "general", PriceCents: 30000, QuantityTotal: 2000, MaxPerOrder: 10}, {Name: "VIP", Category: "vip", PriceCents: 80000, QuantityTotal: 300, MaxPerOrder: 6}}, true)

	comedy := s.event(orgBUser, admin, events.Input{
		OrganiserID: orgB.ID, CategoryID: categories["Comedy"], VenueID: venues["Mother City Theatre"], Title: "Mother City Comedy Night",
		Summary: "Six comedians. One mic. No mercy.", Description: "Cape Town's sharpest stand-up comedians test new material in an intimate theatre setting. Age restriction: 18+.",
		StartsAt: s.local(9, 20, 0), EndsAt: s.local(9, 22, 30), Capacity: 290, RefundsAllowed: true, RefundCutoffHours: 24,
	}, []tt{{Name: "General", Category: "general", PriceCents: 18000, QuantityTotal: 250, MaxPerOrder: 8}, {Name: "Front Row", Category: "vip", PriceCents: 32000, QuantityTotal: 40, MaxPerOrder: 4}}, true)

	summit := s.event(orgBUser, admin, events.Input{
		OrganiserID: orgB.ID, CategoryID: categories["Business & Tech"], VenueID: venues["Harbour Convention Centre"], Title: "Founders & Funders Summit",
		Summary: "Where African start-ups meet capital.", Description: "A full day of talks, pitch sessions and investor meetings for founders building in Africa.",
		StartsAt: s.local(45, 8, 30), EndsAt: s.local(45, 17, 0), Capacity: 230, RefundsAllowed: true, RefundCutoffHours: 168,
	}, []tt{
		{Name: "Early Bird", Category: "early_bird", PriceCents: 120000, QuantityTotal: 50, MaxPerOrder: 5},
		{Name: "Standard", Category: "general", PriceCents: 180000, QuantityTotal: 150, MaxPerOrder: 5},
		{Name: "Student", Category: "student", PriceCents: 45000, QuantityTotal: 30, MaxPerOrder: 1, Description: "Valid student card required at entry"},
	}, true)

	market := s.event(orgBUser, admin, events.Input{
		OrganiserID: orgB.ID, CategoryID: categories["Food & Drink"], VenueID: venues["Harbour Convention Centre"], Title: "Sunday Market Sessions",
		Summary: "Street food, craft beer and live acoustic sets.", Description: "Over 60 food and craft stalls with live acoustic music all afternoon. Re-entry allowed with your ticket.",
		StartsAt: s.local(0, s.real.In(s.loc).Hour()-2, 0), EndsAt: s.local(0, min(s.real.In(s.loc).Hour()+5, 23), 59), Capacity: 400, AllowReentry: true,
	}, []tt{{Name: "Entry", Category: "general", PriceCents: 8000, QuantityTotal: 400, MaxPerOrder: 10}}, true)

	stargazing := s.event(orgBUser, admin, events.Input{
		OrganiserID: orgB.ID, CategoryID: categories["Arts & Culture"], VenueID: venues["Karoo Sky Lodge"], Title: "Karoo Stargazing Retreat",
		Summary: "A night under the clearest skies in the country.", Description: "Guided telescope sessions with an astronomer, followed by a fireside dinner.",
		StartsAt: s.local(14, 19, 0), EndsAt: s.local(14, 23, 30), Capacity: 60, RefundsAllowed: true, RefundCutoffHours: 48,
	}, []tt{{Name: "Retreat Pass", Category: "general", PriceCents: 65000, QuantityTotal: 60, MaxPerOrder: 4}}, true)

	pending := s.event(orgAUser, nil, events.Input{
		OrganiserID: orgA.ID, CategoryID: categories["Food & Drink"], VenueID: venues["Umhlanga Waterfront Pavilion"], Title: "Durban Food & Wine Showcase",
		Summary: "Tastings from 40 estates and KZN's best kitchens.", Description: "Sample award-winning wines alongside dishes from Durban's top chefs.",
		StartsAt: s.local(60, 13, 0), EndsAt: s.local(60, 19, 0), Capacity: 500, RefundsAllowed: true, RefundCutoffHours: 48,
	}, []tt{{Name: "Tasting Pass", Category: "general", PriceCents: 39500, QuantityTotal: 500, MaxPerOrder: 6}}, false)
	must(s.a.Events.Submit(ctx, orgAUser, pending.ID))

	draft := must(s.a.Events.Create(ctx, orgAUser, events.Input{
		OrganiserID: orgA.ID, CategoryID: categories["Music"], VenueID: venues["Jozi Dome Arena"], Title: "Winter Blues Sessions",
		Summary: "Blues and soul to warm up the winter.", Description: "Draft – line-up to be confirmed.",
		StartsAt: s.local(90, 19, 30), EndsAt: s.local(90, 23, 0), Capacity: 350, RefundsAllowed: true, RefundCutoffHours: 48,
	}))
	must(s.a.Events.CreateTicketType(ctx, orgAUser, draft.ID, tt{Name: "General", PriceCents: 28000, QuantityTotal: 350}))

	// --- A fuller catalogue, so every part of the marketplace has something to show -----
	type listing struct {
		org                  *auth.Principal
		organiserID          int64
		category, venue      string
		title, summary, desc string
		day, hour, hours     int
		capacity             int
		types                []tt
		sell                 int // demo purchases, which make some events "selling fast"
	}
	general := func(price int64, qty int) []tt {
		return []tt{{Name: "General Admission", Category: "general", PriceCents: price, QuantityTotal: qty, MaxPerOrder: 8}}
	}
	withVIP := func(price, vip int64, qty, vipQty int) []tt {
		return []tt{
			{Name: "General Admission", Category: "general", PriceCents: price, QuantityTotal: qty, MaxPerOrder: 8},
			{Name: "VIP", Category: "vip", PriceCents: vip, QuantityTotal: vipQty, MaxPerOrder: 4, Description: "Priority entry and a reserved viewing area"},
		}
	}
	catalogue := []listing{
		{orgAUser, orgA.ID, "Music", "Highveld Stadium", "Mzansi Live: The Homecoming", "A stadium night of South Africa's biggest hitmakers.",
			"Five headline acts, one stage and a full stadium singing along. Gates open three hours before the first act.", 6, 17, 6, 120, withVIP(45000, 120000, 100, 20), 95},
		{orgAUser, orgA.ID, "Music", "Florida Road Courtyard", "Durban Soul Sundays", "Neo-soul and R&B in a candlelit courtyard.",
			"An intimate afternoon of soul music with a rotating line-up of Durban vocalists and a live band.", 4, 15, 5, 150, general(19000, 150), 40},
		{orgBUser, orgB.ID, "Music", "Mother City Theatre", "Unplugged at the Theatre", "Acoustic sets from Cape Town's songwriters.",
			"Three songwriters, three acoustic sets and the stories behind the songs.", 17, 19, 3, 200, general(22000, 200), 0},
		{orgAUser, orgA.ID, "Festivals", "Highveld Stadium", "Jacaranda Lights Festival", "Pretoria's spring festival of music and light.",
			"Live music on two stages, light installations, food markets and a lantern parade under the jacarandas.", 27, 14, 9, 300, withVIP(35000, 75000, 260, 40), 160},
		{orgBUser, orgB.ID, "Comedy", "Bay Playhouse", "Laugh Out Loud: Gqeberha", "The country's touring comedy showcase lands in the Bay.",
			"Four comedians, one host and a night of brand-new material. Age restriction: 16+.", 3, 20, 2, 180, general(15000, 180), 55},
		{orgBUser, orgB.ID, "Comedy", "Rose City Hall", "Stand-Up Bloem", "Free State comedy talent, live and unfiltered.",
			"The best of the Free State's stand-up scene with a special guest headliner.", 24, 19, 2, 220, general(12000, 220), 0},
		{orgBUser, orgB.ID, "Theatre", "Mother City Theatre", "The Last Train to Langa", "A new South African drama, now in its final weeks.",
			"An award-winning cast tells the story of one family across three generations. Running time two hours with an interval.", 7, 19, 3, 260, withVIP(26000, 42000, 220, 40), 70},
		{orgBUser, orgB.ID, "Theatre", "Bay Playhouse", "Shakespeare in the Bay", "Twelfth Night, reimagined on the Eastern Cape coast.",
			"A fresh, music-filled production of Shakespeare's comedy of mistaken identities.", 31, 18, 3, 240, general(18000, 240), 0},
		{orgAUser, orgA.ID, "Sport", "Highveld Stadium", "Highveld Derby Day", "The derby the whole city waits for.",
			"Gauteng's fiercest football rivalry, with family zones, food courts and a pre-match concert.", 12, 15, 3, 1500, withVIP(9000, 35000, 1400, 100), 120},
		{orgBUser, orgB.ID, "Sport", "Harbour Convention Centre", "Cape Town Corporate Fun Run", "A 5 km and 10 km route along the Atlantic seaboard.",
			"Race packs, timing chips and a finish-line festival. Collection details are emailed with your ticket.", 22, 7, 4, 800, general(25000, 800), 30},
		{orgBUser, orgB.ID, "Food & Drink", "Florida Road Courtyard", "Durban Curry Trail", "Seven kitchens, seven curries, one walking tour.",
			"A guided evening walk through Durban's most celebrated curry kitchens, with tastings at every stop.", 10, 17, 4, 60, general(42000, 60), 48},
		{orgBUser, orgB.ID, "Business & Tech", "Rose City Hall", "AgriTech Forum", "Technology for the farms that feed the country.",
			"Talks and demonstrations on precision farming, water management and supply chains.", 52, 9, 8, 300, general(65000, 300), 0},
	}
	var extras []events.Event
	for _, l := range catalogue {
		start := s.local(l.day, l.hour, 0)
		end := s.local(l.day, min(l.hour+l.hours, 23), map[bool]int{true: 59, false: 0}[l.hour+l.hours > 23])
		extras = append(extras, s.event(l.org, admin, events.Input{
			OrganiserID: l.organiserID, CategoryID: categories[l.category], VenueID: venues[l.venue], Title: l.title,
			Summary: l.summary, Description: l.desc, StartsAt: start, EndsAt: end, Capacity: l.capacity,
			RefundsAllowed: l.category != "Sport", RefundCutoffHours: 48,
		}, l.types, true))
	}

	// --- Sales over the last six weeks ---------------------------------------------------
	cust := func(i int) string { return customers[i%len(customers)] }
	types := func(e events.Event) []events.TicketType { return must(s.a.Events.TicketTypes(ctx, admin, e.ID)) }

	for i, e := range extras {
		ets := types(e)
		for n := 0; n < catalogue[i].sell; n += 4 {
			s.at(-20+n%18, 9+float64(n%10))
			s.buy(cust(n+i), e, []sales.ReserveItem{{TicketTypeID: ets[0].ID, Quantity: 4}}, "", sales.MethodOnline)
		}
	}

	springTypes := types(spring)
	for i := 0; i < 26; i++ {
		s.at(-38+i, 10+float64(i%8))
		s.buy(cust(i), spring, []sales.ReserveItem{{TicketTypeID: springTypes[i%2].ID, Quantity: 1 + i%4}}, "", sales.MethodOnline)
	}
	jazzTypes := types(jazz)
	for i := 0; i < 34; i++ {
		s.at(-30+i*30/34, 8+float64(i%12))
		item := jazzTypes[1]
		if i < 14 {
			item = jazzTypes[0] // early bird, before its sales window closes
		} else if i%5 == 0 {
			item = jazzTypes[2]
		}
		promo := ""
		if i%6 == 3 {
			promo = "JAZZ15"
		}
		method := sales.MethodOnline
		if i%9 == 4 {
			method = sales.MethodEFT
		}
		orderID := s.buy(cust(i+3), jazz, []sales.ReserveItem{{TicketTypeID: item.ID, Quantity: 1 + i%3}}, promo, method)
		if method == sales.MethodEFT && orderID != 0 {
			s.clock.Advance(20 * time.Hour) // the transfer reflects the next day
			s.confirmEFT(orderID)
		}
	}
	amaTypes := types(amapiano)
	for i := 0; i < 28; i++ {
		s.at(-25+i*24/28, 9+float64(i%10))
		s.buy(cust(i+7), amapiano, []sales.ReserveItem{{TicketTypeID: amaTypes[i%3/2].ID, Quantity: 2 + i%4}}, "", sales.MethodOnline)
	}
	comedyTypes := types(comedy)
	for i := 0; i < 22; i++ {
		s.at(-20+i*19/22, 11+float64(i%9))
		s.buy(cust(i+11), comedy, []sales.ReserveItem{{TicketTypeID: comedyTypes[i%4/3].ID, Quantity: 1 + i%3}}, "", sales.MethodOnline)
	}
	summitTypes := types(summit)
	for i := 0; i < 12; i++ {
		s.at(-15+i, 9+float64(i%7))
		s.buy(cust(i+2), summit, []sales.ReserveItem{{TicketTypeID: summitTypes[i%3].ID, Quantity: 1}}, "", sales.MethodOnline)
	}
	marketTypes := types(market)
	for i := 0; i < 30; i++ {
		s.at(-10+i/3, 7+float64(i%12))
		s.buy(cust(i+5), market, []sales.ReserveItem{{TicketTypeID: marketTypes[0].ID, Quantity: 1 + i%4}}, "", sales.MethodOnline)
	}
	starTypes := types(stargazing)
	var starOrders []int64
	for i := 0; i < 8; i++ {
		s.at(-9+i, 12)
		starOrders = append(starOrders, s.buy(cust(i+20), stargazing, []sales.ReserveItem{{TicketTypeID: starTypes[0].ID, Quantity: 2}}, "", sales.MethodOnline))
	}

	// Complimentary tickets for the press.
	s.at(-6, 14)
	must(s.a.Sales.IssueComplimentary(ctx, orgAUser, jazz.ID, sales.ComplimentaryInput{TicketTypeID: jazzTypes[2].ID, Quantity: 4,
		FirstName: "Press", LastName: "Desk", Email: "press@jozijazz.example", Note: "Media partners"}))

	finance := s.principal("finance@gotaticket.test")
	// One fresh EFT order awaiting payment, for finance to confirm in the demo.
	s.at(0, -3)
	s.buy("customer@gotaticket.test", amapiano, []sales.ReserveItem{{TicketTypeID: amaTypes[1].ID, Quantity: 2}}, "", sales.MethodEFT)

	// --- Attendance ----------------------------------------------------------------------
	door := s.principal("checkin@gotaticket.test")
	s.at(-12, -float64(s.real.In(s.loc).Hour())+18.6) // ~18:36 on the event day
	s.checkInAll(door, spring.ID, 0.86)
	s.at(0, -0.5)
	s.checkInAll(s.principal("checkin@gotaticket.test"), market.ID, 0.42)

	// --- Refunds -------------------------------------------------------------------------
	s.at(-3, 10)
	comedyOrders := must2(s.a.Sales.ListOrders(ctx, orgBUser, sales.OrderFilter{EventID: comedy.ID, Status: sales.OrderPaid}, pageAll()))
	if len(comedyOrders) > 1 {
		owner := s.principalByOrder(comedyOrders[0].ID)
		must(s.a.Sales.RequestRefund(ctx, owner, comedyOrders[0].ID, sales.RefundRequestInput{Reason: "I can no longer attend on that date."}))
		ref := must(s.a.Sales.CreateRefund(ctx, orgBUser, comedyOrders[1].ID, sales.RefundRequestInput{Reason: "Customer booked twice by mistake."}))
		must(s.a.Sales.ProcessRefund(ctx, finance, ref.ID, sales.ProcessRefundInput{}))
	}
	jazzOrders := must2(s.a.Sales.ListOrders(ctx, orgAUser, sales.OrderFilter{EventID: jazz.ID, Status: sales.OrderPaid}, pageAll()))
	if len(jazzOrders) > 2 {
		owner := s.principalByOrder(jazzOrders[2].ID)
		if _, err := s.a.Sales.RequestRefund(ctx, owner, jazzOrders[2].ID, sales.RefundRequestInput{Reason: "Family emergency."}); err == nil {
			s.a.Logger.Debug("refund requested")
		}
	}

	// The stargazing retreat is cancelled because of the weather: every buyer gets a refund.
	s.at(-1, 16)
	must(s.a.Events.Cancel(ctx, orgBUser, stargazing.ID, "Severe weather warning for the Karoo"))
	refunds := must2(s.a.Sales.ListRefunds(ctx, finance, sales.RefundFilter{EventID: stargazing.ID, Status: "approved"}, pageAll()))
	for i, r := range refunds {
		if i >= len(refunds)/2 {
			break
		}
		must(s.a.Sales.ProcessRefund(ctx, finance, r.ID, sales.ProcessRefundInput{}))
	}
	s.a.Logger.Info("demo purchases created", "stargazing_orders", len(starOrders))
	return nil
}

func (s *seeder) createUser(email, first, last, role string) {
	now := s.clock.Now()
	res, err := s.a.DB.ExecContext(s.ctx, `INSERT INTO users (email, password_hash, first_name, last_name, phone, status, email_verified_at, password_changed_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, '', 'active', ?, ?, ?, ?)`, email, s.hash, first, last, now, now, now, now)
	if err != nil {
		panic(fmt.Sprintf("seed: create user %s: %v", email, err))
	}
	id, _ := res.LastInsertId()
	if _, err := s.a.DB.ExecContext(s.ctx, `INSERT INTO user_roles (user_id, role_id, created_at) SELECT ?, id, ? FROM roles WHERE slug = ?`, id, now, role); err != nil {
		panic(err)
	}
}

func (s *seeder) superAdminCount() int {
	var n int
	_ = s.a.DB.QueryRowContext(s.ctx, `SELECT COUNT(*) FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE r.slug = ?`, rbac.RoleSuperAdmin).Scan(&n)
	return n
}

// event creates an event with ticket types and, when approve is true, submits and publishes it.
func (s *seeder) event(owner, reviewer *auth.Principal, in events.Input, types []events.TicketTypeInput, approve bool) events.Event {
	if in.ImageURL == "" {
		art := must(eventArtwork(s.categoryNames[in.CategoryID], in.Title))
		in.ImageURL = must(s.a.Storage.SaveImage(s.ctx, bytes.NewReader(art), 5<<20))
	}
	e := must(s.a.Events.Create(s.ctx, owner, in))
	for i, t := range types {
		t.SortOrder = i
		if t.MinPerOrder == 0 {
			t.MinPerOrder = 1
		}
		must(s.a.Events.CreateTicketType(s.ctx, owner, e.ID, t))
	}
	if approve {
		must(s.a.Events.Submit(s.ctx, owner, e.ID))
		e = must(s.a.Events.Approve(s.ctx, reviewer, e.ID, "Looks great."))
	}
	return e
}

// buy runs a real purchase: reserve, checkout, and either pay through the sandbox gateway
// or choose EFT. It returns the order ID.
func (s *seeder) buy(email string, e events.Event, items []sales.ReserveItem, promo, method string) int64 {
	customer := s.principal(email)
	order, err := s.a.Sales.Reserve(s.ctx, customer, sales.ReserveInput{EventID: e.ID, Items: items, PromoCode: promo})
	if err != nil {
		s.a.Logger.Debug("demo purchase skipped", "event", e.Title, "error", err)
		return 0
	}
	var attendees []sales.AttendeeInput
	for _, it := range order.Items {
		for i := 0; i < it.Quantity; i++ {
			first, last := customer.FirstName, customer.LastName
			if i > 0 {
				first = []string{"Sam", "Alex", "Jordan", "Taylor", "Morgan"}[i%5]
			}
			attendees = append(attendees, sales.AttendeeInput{OrderItemID: it.ID, FirstName: first, LastName: last, Email: customer.Email})
		}
	}
	result := must(s.a.Sales.Checkout(s.ctx, customer, order.ID, sales.CheckoutInput{Attendees: attendees, PaymentMethod: method}))
	if result.Next == "redirect" {
		reference := result.Order.Payments[0].ProviderReference
		if _, err := s.a.Sandbox.Complete(s.ctx, reference, true); err != nil {
			panic(err)
		}
		if err := s.a.Sales.SyncPayment(s.ctx, reference); err != nil {
			panic(err)
		}
	}
	return order.ID
}

// confirmEFT records the bank transfer for an order's pending EFT payment, as finance would.
func (s *seeder) confirmEFT(orderID int64) {
	var paymentID, amount int64
	var reference string
	if err := s.a.DB.QueryRowContext(s.ctx, `SELECT p.id, p.amount_cents, o.reference FROM payments p JOIN orders o ON o.id = p.order_id
		WHERE p.order_id = ? AND p.method = 'eft' AND p.status = 'pending'`, orderID).Scan(&paymentID, &amount, &reference); err != nil {
		panic(err)
	}
	must(s.a.Sales.ConfirmEFT(s.ctx, s.principal("finance@gotaticket.test"), paymentID,
		sales.ConfirmEFTInput{AmountCents: amount, BankReference: "FNB-" + reference}))
}

func (s *seeder) principalByOrder(orderID int64) *auth.Principal {
	var email string
	_ = s.a.DB.QueryRowContext(s.ctx, `SELECT u.email FROM orders o JOIN users u ON u.id = o.customer_id WHERE o.id = ?`, orderID).Scan(&email)
	return s.principal(email)
}

// checkInAll scans a share of an event's valid tickets through the real check-in service.
func (s *seeder) checkInAll(door *auth.Principal, eventID int64, share float64) {
	rows, err := s.a.DB.QueryContext(s.ctx, `SELECT code, qr_version FROM tickets WHERE event_id = ? AND status = 'valid' ORDER BY id`, eventID)
	if err != nil {
		panic(err)
	}
	type ticket struct {
		code    string
		version int
	}
	var list []ticket
	for rows.Next() {
		var t ticket
		if err := rows.Scan(&t.code, &t.version); err != nil {
			panic(err)
		}
		list = append(list, t)
	}
	rows.Close()
	limit := int(float64(len(list)) * share)
	for i, t := range list[:limit] {
		s.clock.Advance(time.Duration(20+i%40) * time.Second)
		must(s.a.Checkin.Scan(s.ctx, door, checkin.ScanInput{EventID: eventID, Code: s.a.Signer.Payload(t.code, t.version), Direction: checkin.DirectionIn, Gate: "Main gate"}))
	}
	// A duplicate scan attempt, which the platform rejects.
	if limit > 0 {
		must(s.a.Checkin.Scan(s.ctx, door, checkin.ScanInput{EventID: eventID, Code: s.a.Signer.Payload(list[0].code, list[0].version), Direction: checkin.DirectionIn, Gate: "Main gate"}))
	}
}

func (s *seeder) printSummary() {
	fmt.Println()
	fmt.Println("Demo data loaded. Every demo account uses the password:", DemoPassword)
	fmt.Println()
	accounts := [][2]string{}
	if s.createdSuperAdmin {
		accounts = append(accounts, [2]string{"superadmin@gotaticket.test", "Super Administrator"})
	}
	for _, line := range append(accounts, [][2]string{
		{"admin@gotaticket.test", "Administrator"},
		{"finance@gotaticket.test", "Finance Manager"},
		{"ticketing@gotaticket.test", "Ticketing Officer"},
		{"checkin@gotaticket.test", "Check-in Officer (all organisers)"},
		{"organiser@gotaticket.test", "Event Organiser – Soweto Sound Productions"},
		{"organiser2@gotaticket.test", "Event Organiser – Cape Town Comedy Collective"},
		{"doorstaff@gotaticket.test", "Organiser check-in staff – Soweto Sound Productions"},
		{"marketing@gotaticket.test", "Custom role: Marketing Manager"},
		{"customer@gotaticket.test", "Customer"},
	}...) {
		fmt.Printf("  %-28s %s\n", line[0], line[1])
	}
	fmt.Println()
}

func slugify(s string) string {
	return strings.Trim(strings.NewReplacer(" & ", "-", " ", "-").Replace(strings.ToLower(s)), "-")
}

func intPtr(v int) *int { return &v }

func must2[T any](v T, _ int64, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func pageAll() httpx.Page { return httpx.Page{Number: 1, Size: 100} }
