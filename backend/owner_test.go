package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	log "github.com/hashicorp/go-hclog"
	"github.com/openbao/openbao/sdk/v2/logical"
)

const (
	entityA = "11111111-1111-1111-1111-111111111111"
	entityB = "22222222-2222-2222-2222-222222222222"
	entityC = "33333333-3333-3333-3333-333333333333"
)

// groupView answers GroupsForEntity per entity, as Bao's identity store does.
type groupView struct {
	logical.StaticSystemView
	groups map[string][]*logical.Group
}

func (g *groupView) GroupsForEntity(entityID string) ([]*logical.Group, error) {
	return g.groups[entityID], nil
}

var team = &logical.Group{ID: "44444444-4444-4444-4444-444444444444", Name: "team"}

var testSystem = &groupView{groups: map[string][]*logical.Group{
	entityA: {team},
	entityB: {team},
}}

// setupOwnerBackend is a plain-mode backend with a catalog of one key/value
// table and one blob table, and the caps given (as env strings; "" = default).
func setupOwnerBackend(t *testing.T, maxPages, execsPerMin string) (*backend, *logical.InmemStorage) {
	t.Helper()
	dir := t.TempDir()
	catPath := filepath.Join(dir, "catalog.hcl")
	if err := os.WriteFile(catPath, []byte(`
schema "kv" {
  sql = "CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT NOT NULL)"
}
schema "blob" {
  sql = "CREATE TABLE IF NOT EXISTS blob (n INTEGER PRIMARY KEY, v BLOB NOT NULL)"
}
exec "put" {
  sql  = "INSERT INTO kv (k, v) VALUES (?, ?) ON CONFLICT (k) DO UPDATE SET v = excluded.v"
  args = ["k", "v"]
}
exec "blob_put" {
  sql  = "INSERT INTO blob (v) VALUES (zeroblob(?))"
  args = ["size"]
}
query "all" {
  sql = "SELECT k, v FROM kv ORDER BY k"
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envCatalog, catPath)
	t.Setenv(envDB, "")
	t.Setenv(envCluster, "")
	t.Setenv(envDSN, "")
	t.Setenv(envDir, dir)
	t.Setenv(envMaxPages, maxPages)
	t.Setenv(envExecsPerMin, execsPerMin)

	storage := &logical.InmemStorage{}
	lb, err := Factory(context.Background(), &logical.BackendConfig{
		Logger:      log.NewNullLogger(),
		StorageView: storage,
		System:      testSystem,
	})
	if err != nil {
		t.Fatal(err)
	}
	return lb.(*backend), storage
}

func call(b *backend, storage *logical.InmemStorage, entity string, op logical.Operation, path string,
	data map[string]interface{}) (*logical.Response, error) {
	return b.HandleRequest(context.Background(), &logical.Request{
		Operation: op, Path: path, Data: data, Storage: storage, EntityID: entity,
	})
}

func put(t *testing.T, b *backend, s *logical.InmemStorage, entity, db, k, v string) {
	t.Helper()
	resp, err := call(b, s, entity, logical.UpdateOperation, "exec/"+db+"/put",
		map[string]interface{}{"k": k, "v": v})
	if err != nil || resp.IsError() {
		t.Fatalf("put as %s into %s: %v %v", entity, db, err, resp)
	}
}

func keys(t *testing.T, b *backend, s *logical.InmemStorage, entity, db string) []string {
	t.Helper()
	resp, err := call(b, s, entity, logical.ReadOperation, "query/"+db+"/all", nil)
	if err != nil || resp.IsError() {
		t.Fatalf("read as %s from %s: %v %v", entity, db, err, resp)
	}
	var out []string
	for _, row := range resp.Data["rows"].([]map[string]interface{}) {
		out = append(out, row["k"].(string))
	}
	return out
}

// Two identities naming the same database each get their own; neither reads the
// other's rows, even by writing the other's physical name.
func TestOwner_IdentitiesDoNotShareADatabase(t *testing.T) {
	b, s := setupOwnerBackend(t, "", "")
	put(t, b, s, entityA, "kv", "a-secret", "1")

	if got := keys(t, b, s, entityB, "kv"); len(got) != 0 {
		t.Fatalf("B read A's rows: %v", got)
	}
	resp, err := call(b, s, entityB, logical.ReadOperation, "query/e-"+entityA+"--kv/all", nil)
	if err == nil && !resp.IsError() {
		if rows := resp.Data["rows"].([]map[string]interface{}); len(rows) != 0 {
			t.Fatalf("B reached A's database by its physical name: %v", rows)
		}
	}
	if got := keys(t, b, s, entityA, "kv"); len(got) != 1 || got[0] != "a-secret" {
		t.Fatalf("A lost its own row: %v", got)
	}
}

// Control: with the identity binding planted out, the same test must see B read
// A's row, so the test above can fail.
func TestOwner_ControlWithoutBindingLeaks(t *testing.T) {
	b, s := setupOwnerBackend(t, "", "")
	b.unbound = true
	put(t, b, s, entityA, "kv", "a-secret", "1")
	if got := keys(t, b, s, entityB, "kv"); len(got) != 1 {
		t.Fatalf("the planted leak did not show; the isolation test cannot fail: %v", got)
	}
}

func TestOwner_TokenWithoutIdentityIsRefused(t *testing.T) {
	b, s := setupOwnerBackend(t, "", "")
	for _, op := range []struct {
		op   logical.Operation
		path string
	}{
		{logical.UpdateOperation, "exec/kv/put"},
		{logical.ReadOperation, "query/kv/all"},
	} {
		_, err := call(b, s, "", op.op, op.path, map[string]interface{}{"k": "x", "v": "y"})
		if !errors.Is(err, logical.ErrPermissionDenied) {
			t.Fatalf("%s with no identity: err=%v, want permission denied", op.path, err)
		}
	}
}

// Members of a group share its database; a non-member is refused.
func TestOwner_GroupDatabaseIsSharedByMembersOnly(t *testing.T) {
	b, s := setupOwnerBackend(t, "", "")
	put(t, b, s, entityA, "group.team.kv", "shared", "1")
	if got := keys(t, b, s, entityB, "group.team.kv"); len(got) != 1 || got[0] != "shared" {
		t.Fatalf("B, a member, does not see the group row: %v", got)
	}
	_, err := call(b, s, entityC, logical.ReadOperation, "query/group.team.kv/all", nil)
	if !errors.Is(err, logical.ErrPermissionDenied) {
		t.Fatalf("C, not a member: err=%v, want permission denied", err)
	}
	if got := keys(t, b, s, entityA, "kv"); len(got) != 0 {
		t.Fatalf("the group row leaked into A's own database: %v", got)
	}
}

func fillBlobs(b *backend, s *logical.InmemStorage, n int) error {
	for i := 0; i < n; i++ {
		resp, err := call(b, s, entityA, logical.UpdateOperation, "exec/blob/blob_put",
			map[string]interface{}{"size": 4096})
		if err != nil {
			return err
		}
		if resp.IsError() {
			return errors.New(resp.Error().Error())
		}
	}
	return nil
}

// A database at its page cap refuses the next write; the same writes with the
// cap off go through.
func TestCaps_PageCapRefusesGrowth(t *testing.T) {
	b, s := setupOwnerBackend(t, "16", "0")
	err := fillBlobs(b, s, 40)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "full") {
		t.Fatalf("40 pages of blobs under a 16-page cap: err=%v, want database full", err)
	}

	b, s = setupOwnerBackend(t, "0", "0")
	if err := fillBlobs(b, s, 40); err != nil {
		t.Fatalf("control, cap off: %v", err)
	}
}

// An owner over the exec rate is refused; another owner is not; with the cap
// off the same burst goes through.
func TestCaps_ExecRateCapPerOwner(t *testing.T) {
	b, s := setupOwnerBackend(t, "", "3")
	for i := 0; i < 3; i++ {
		put(t, b, s, entityA, "kv", "k", "v")
	}
	_, err := call(b, s, entityA, logical.UpdateOperation, "exec/kv/put",
		map[string]interface{}{"k": "k", "v": "v"})
	if !errors.Is(err, logical.ErrRateLimitQuotaExceeded) {
		t.Fatalf("4th exec in a minute under a cap of 3: err=%v, want rate limit", err)
	}
	put(t, b, s, entityB, "kv", "k", "v")

	b, s = setupOwnerBackend(t, "", "0")
	for i := 0; i < 10; i++ {
		put(t, b, s, entityA, "kv", "k", "v")
	}
}

// storedEntries counts what the engine has written to Bao's own storage.
func storedEntries(t *testing.T, s *logical.InmemStorage) int {
	t.Helper()
	ks, err := logical.CollectKeys(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return len(ks)
}

// Growth gate (RFD66606-2316): many requests leave Bao's storage as it was. The
// control stores one entry and must be seen, so the gate can fail.
func TestGrowth_RequestsLeaveBaoStorageUnchanged(t *testing.T) {
	b, s := setupOwnerBackend(t, "", "0")
	before := storedEntries(t, s)
	for i := 0; i < 50; i++ {
		put(t, b, s, entityA, "kv", "k", "v")
		keys(t, b, s, entityA, "kv")
	}
	if after := storedEntries(t, s); after != before {
		t.Fatalf("Bao storage grew from %d to %d entries over 100 requests", before, after)
	}

	if err := s.Put(context.Background(), &logical.StorageEntry{Key: "lease/1", Value: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if storedEntries(t, s) == before {
		t.Fatal("control: a stored entry was not counted, so the growth gate cannot fail")
	}
}
