// Package auth authenticates requests and answers authorisation questions.
//
// A Principal carries two kinds of permissions:
//
//   - platform permissions, from roles assigned directly to the user, and
//   - organiser permissions, from the role the user holds in each organiser they belong to.
//
// Organiser-owned data (events, orders, attendees, reports) is visible to a user when they
// hold the permission through the organiser's membership, or hold it as a platform
// permission together with platform.all_organisers. This keeps tenants isolated: a role
// granted inside one organiser never reaches another organiser's data.
package auth

import (
	"context"
	"sort"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

// Principal is the authenticated user and what they may do.
type Principal struct {
	UserID       int64
	Email        string
	FirstName    string
	LastName     string
	IsSuperAdmin bool
	platform     map[string]bool
	organisers   map[int64]map[string]bool
}

// NewPrincipal builds a principal from its permission sets.
func NewPrincipal(userID int64, email, first, last string, superAdmin bool, platform []string, organisers map[int64][]string) *Principal {
	p := &Principal{
		UserID: userID, Email: email, FirstName: first, LastName: last, IsSuperAdmin: superAdmin,
		platform:   map[string]bool{},
		organisers: map[int64]map[string]bool{},
	}
	for _, code := range platform {
		p.platform[code] = true
	}
	for orgID, codes := range organisers {
		set := map[string]bool{}
		for _, code := range codes {
			set[code] = true
		}
		p.organisers[orgID] = set
	}
	return p
}

// HasPlatform reports a permission granted through a platform role.
func (p *Principal) HasPlatform(perm string) bool { return p.platform[perm] }

// AllOrganisers reports whether platform permissions apply to every organiser.
func (p *Principal) AllOrganisers() bool { return p.platform[rbac.AllOrganisers] }

// CanFor reports whether the user holds perm for data owned by organiserID.
func (p *Principal) CanFor(organiserID int64, perm string) bool {
	if p.AllOrganisers() && p.platform[perm] {
		return true
	}
	return p.organisers[organiserID][perm]
}

// CanAny reports whether the user holds perm anywhere; used to gate routes before the
// specific organiser is known.
func (p *Principal) CanAny(perm string) bool {
	if p.platform[perm] {
		return true
	}
	for _, set := range p.organisers {
		if set[perm] {
			return true
		}
	}
	return false
}

// HasStaffAccess reports whether the user holds any permission at all (i.e. may open the
// admin portal).
func (p *Principal) HasStaffAccess() bool {
	if len(p.platform) > 0 {
		return true
	}
	for _, set := range p.organisers {
		if len(set) > 0 {
			return true
		}
	}
	return false
}

// IsMember reports organiser membership.
func (p *Principal) IsMember(organiserID int64) bool {
	_, ok := p.organisers[organiserID]
	return ok
}

// Scope returns which organisers' data the user may access with perm.
func (p *Principal) Scope(perm string) Scope {
	if p.AllOrganisers() && p.platform[perm] {
		return Scope{All: true}
	}
	var ids []int64
	for orgID, set := range p.organisers {
		if set[perm] {
			ids = append(ids, orgID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return Scope{IDs: ids}
}

// PlatformPermissions returns the sorted platform permission codes.
func (p *Principal) PlatformPermissions() []string { return sortedKeys(p.platform) }

// OrganiserPermissions returns the sorted permission codes held in organiserID.
func (p *Principal) OrganiserPermissions(organiserID int64) []string {
	return sortedKeys(p.organisers[organiserID])
}

// OrganiserIDs returns the organisers the user is an active member of.
func (p *Principal) OrganiserIDs() []int64 {
	ids := make([]int64, 0, len(p.organisers))
	for id := range p.organisers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Scope is the set of organisers a query may read.
type Scope struct {
	All bool
	IDs []int64
}

// Empty reports that no organiser data is accessible.
func (s Scope) Empty() bool { return !s.All && len(s.IDs) == 0 }

// Includes reports whether organiserID is inside the scope.
func (s Scope) Includes(organiserID int64) bool {
	if s.All {
		return true
	}
	for _, id := range s.IDs {
		if id == organiserID {
			return true
		}
	}
	return false
}

// SQL returns a WHERE fragment restricting column to the scope, with its arguments.
func (s Scope) SQL(column string) (string, []any) {
	switch {
	case s.All:
		return "1 = 1", nil
	case len(s.IDs) == 0:
		return "1 = 0", nil
	default:
		return column + " IN (" + database.Placeholders(len(s.IDs)) + ")", database.Args(s.IDs)
	}
}

// Narrow restricts the scope to a single organiser when one is requested (filter value
// non-zero). Asking for an organiser outside the scope yields an empty scope.
func (s Scope) Narrow(organiserID int64) Scope {
	if organiserID == 0 {
		return s
	}
	if s.Includes(organiserID) {
		return Scope{IDs: []int64{organiserID}}
	}
	return Scope{}
}

type principalKey struct{}

// WithPrincipal stores p in ctx.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns the authenticated principal, or nil for anonymous requests.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}
