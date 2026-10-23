package backend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openbao/openbao/sdk/v2/logical"
)

// errDenied marks a resolve failure that is about who is asking (no identity, not
// a member), answered as permission denied; any other failure is a bad request.
var errDenied = errors.New("denied")

// groupPrefix marks a database shared by a Bao identity group: group.<group>.<db>.
const groupPrefix = "group."

// ownedDB is a named database resolved against the caller's identity. logical is
// the name the caller wrote and the catalog's schemas match; physical is the
// name opened in FDB or on disk, which carries the owner so two callers that
// write the same name never meet.
type ownedDB struct {
	logical  string
	physical string
	owner    string
}

// resolveDB binds a database named in a request path to the caller's Bao
// identity. A plain name is the caller's own database; group.<g>.<db> is the
// database of group <g>, open only to its members. A token with no entity
// (root, or one made without an identity) owns no database and is refused.
func (b *backend) resolveDB(req *logical.Request, db string) (ownedDB, error) {
	if req.EntityID == "" {
		return ownedDB{}, fmt.Errorf("%w: named databases belong to a Bao identity; this token has none", errDenied)
	}
	if b.unbound {
		return ownedDB{logical: db, physical: db, owner: db}, nil
	}
	if !strings.HasPrefix(db, groupPrefix) {
		if !dbNamePattern.MatchString(db) {
			return ownedDB{}, fmt.Errorf("database name %q is not [A-Za-z0-9_-]", db)
		}
		owner := "e-" + req.EntityID
		return ownedDB{logical: db, physical: owner + "--" + db, owner: owner}, nil
	}
	parts := strings.SplitN(strings.TrimPrefix(db, groupPrefix), ".", 2)
	if len(parts) != 2 || !dbNamePattern.MatchString(parts[0]) || !dbNamePattern.MatchString(parts[1]) {
		return ownedDB{}, fmt.Errorf("group database %q is not group.<group>.<db>", db)
	}
	groupName, logicalName := parts[0], parts[1]
	sys := b.System()
	if sys == nil {
		return ownedDB{}, fmt.Errorf("group databases need the system view")
	}
	groups, err := sys.GroupsForEntity(req.EntityID)
	if err != nil {
		return ownedDB{}, fmt.Errorf("read the caller's groups: %w", err)
	}
	for _, g := range groups {
		if g != nil && g.Name == groupName {
			owner := "g-" + g.ID
			return ownedDB{logical: logicalName, physical: owner + "--" + logicalName, owner: owner}, nil
		}
	}
	return ownedDB{}, fmt.Errorf("%w: the caller is not a member of group %q", errDenied, groupName)
}

// execLimiter caps exec calls per owner with a token bucket: perMinute calls
// fill it, and it refills at perMinute a minute. Zero turns the cap off.
type execLimiter struct {
	perMinute float64
	mu        sync.Mutex
	buckets   map[string]*bucket
	now       func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newExecLimiter(perMinute int) *execLimiter {
	return &execLimiter{perMinute: float64(perMinute), buckets: map[string]*bucket{}, now: time.Now}
}

// allow spends one token of the owner's bucket and reports whether it had one.
func (l *execLimiter) allow(owner string) bool {
	if l == nil || l.perMinute <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	bk, ok := l.buckets[owner]
	if !ok {
		bk = &bucket{tokens: l.perMinute, last: now}
		l.buckets[owner] = bk
	}
	bk.tokens += now.Sub(bk.last).Minutes() * l.perMinute
	if bk.tokens > l.perMinute {
		bk.tokens = l.perMinute
	}
	bk.last = now
	if bk.tokens < 1 {
		return false
	}
	bk.tokens--
	return true
}

// resolveResponse answers a resolve failure: 403 for errDenied, 400 otherwise.
func resolveResponse(err error) (*logical.Response, error) {
	if errors.Is(err, errDenied) {
		return logical.ErrorResponse("%v", err), logical.ErrPermissionDenied
	}
	return logical.ErrorResponse("%v", err), nil
}

// ownedStore opens the resolved database, applying the schemas its logical name
// matches.
func (b *backend) ownedStore(ctx context.Context, d ownedDB) (Store, error) {
	return b.named(ctx, d.physical, d.logical)
}
