// 本文件用内存夹具验证增改删、幂等、所有权与失败时不更新目录的边界。
package pricing

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeStore struct {
	rows            map[string]OverrideRow
	fail            string
	exists, missing bool
	calls           []string
}

func (f *fakeStore) List(context.Context) ([]OverrideRow, error) {
	f.calls = append(f.calls, "list")
	if f.fail == "list" {
		return nil, errors.New("failed")
	}
	var r []OverrideRow
	for _, v := range f.rows {
		r = append(r, v)
	}
	return r, nil
}
func (f *fakeStore) Create(_ context.Context, r OverrideRow) error {
	f.calls = append(f.calls, "create")
	if f.fail == "create" {
		return errors.New("failed")
	}
	if f.exists {
		return ErrRowExists
	}
	f.rows[r.ID] = r
	return nil
}
func (f *fakeStore) Update(_ context.Context, r OverrideRow) error {
	f.calls = append(f.calls, "update")
	if f.fail == "update" {
		return errors.New("failed")
	}
	f.rows[r.ID] = r
	return nil
}
func (f *fakeStore) Delete(_ context.Context, id string) error {
	f.calls = append(f.calls, "delete")
	if f.fail == "delete" {
		return errors.New("failed")
	}
	delete(f.rows, id)
	if f.missing {
		return ErrRowMissing
	}
	return nil
}

type fakeCatalog struct {
	upserts int
	rows    []OverrideRow
	deleted []string
	fail    bool
}

func (c *fakeCatalog) Upsert(rows ...OverrideRow) error {
	c.upserts++
	if c.fail {
		return errors.New("catalog failed")
	}
	c.rows = rows
	return nil
}
func (c *fakeCatalog) Delete(id string) { c.deleted = append(c.deleted, id) }

type fakeProviders struct {
	rows []Provider
	fail bool
}

func (p *fakeProviders) List(context.Context) ([]Provider, error) {
	if p.fail {
		return nil, errors.New("providers failed")
	}
	return p.rows, nil
}
func syncFixture(t *testing.T) (*Service, *fakeStore, *fakeCatalog, *fakeProviders) {
	t.Helper()
	store := &fakeStore{rows: map[string]OverrideRow{}}
	cat := &fakeCatalog{}
	providers := &fakeProviders{rows: []Provider{{Name: "q", Custom: true, BaseURL: "dashscope.aliyuncs.com"}}}
	s, e := New(testFile(t), Options{}, Deps{store, cat, providers, &testLogger{}})
	if e != nil {
		t.Fatal(e)
	}
	return s, store, cat, providers
}

func TestSyncLifecycle(t *testing.T) {
	s, store, cat, providers := syncFixture(t)
	ctx := context.Background()
	store.rows["manual"] = OverrideRow{ID: "manual", Name: "my price"}
	r, e := s.Sync(ctx)
	if e != nil || r.Created != 1 || cat.upserts != 1 {
		t.Fatalf("%+v %v", r, e)
	}
	r, e = s.Sync(ctx)
	if e != nil || r.Unchanged != 1 || r.Created != 0 || r.Updated != 0 {
		t.Fatalf("%+v %v", r, e)
	}
	s.file.Rates[CNY] = 3.6
	r, e = s.Sync(ctx)
	if e != nil || r.Updated != 1 {
		t.Fatalf("%+v %v", r, e)
	}
	providers.rows = nil
	r, e = s.Sync(ctx)
	if e != nil || r.Deleted != 1 || len(cat.deleted) != 1 || len(store.rows) != 1 || store.rows["manual"].Name != "my price" {
		t.Fatalf("%+v %v %v", r, e, store.rows)
	}
}

func TestSyncIdempotentErrors(t *testing.T) {
	t.Run("create conflict", func(t *testing.T) {
		s, store, _, _ := syncFixture(t)
		store.exists = true
		r, e := s.Sync(context.Background())
		if e != nil || r.Updated != 1 || r.Created != 0 || strings.Join(store.calls, ",") != "list,create,update" {
			t.Fatalf("%+v %v %v", r, e, store.calls)
		}
	})
	t.Run("missing delete", func(t *testing.T) {
		s, store, cat, p := syncFixture(t)
		if _, e := s.Sync(context.Background()); e != nil {
			t.Fatal(e)
		}
		p.rows = nil
		store.missing = true
		r, e := s.Sync(context.Background())
		if e != nil || r.Deleted != 1 || len(cat.deleted) != 1 {
			t.Fatalf("%+v %v", r, e)
		}
	})
}

func TestSyncFailure(t *testing.T) {
	for _, operation := range []string{"providers", "list", "create", "update", "delete", "catalog"} {
		t.Run(operation, func(t *testing.T) {
			s, store, cat, p := syncFixture(t)
			if operation == "update" || operation == "delete" {
				if _, e := s.Sync(context.Background()); e != nil {
					t.Fatal(e)
				}
				cat.upserts = 0
				s.file.Rates[CNY] = 3.6
			}
			if operation == "delete" {
				p.rows = nil
			}
			store.fail = operation
			p.fail = operation == "providers"
			cat.fail = operation == "catalog"
			if _, e := s.Sync(context.Background()); e == nil {
				t.Fatal("failure ignored")
			}
			if operation != "catalog" && cat.upserts != 0 {
				t.Fatal("catalog updated after database failure")
			}
		})
	}
}

func TestSyncOwnership(t *testing.T) {
	s, store, cat, _ := syncFixture(t)
	forged := OverrideRow{ID: "manual-prefixed", Name: "ee-pricing: dashscope/qwen-test", ProviderID: "q", Pattern: "qwen-test"}
	collision := OverrideRow{ID: overrideID("q", "qwen-test"), Name: "manual exact ID", ProviderID: "q", Pattern: "qwen-test"}
	store.rows[forged.ID] = forged
	store.rows[collision.ID] = collision
	r, e := s.Sync(context.Background())
	if e != nil || r.Created+r.Updated+r.Deleted != 0 || len(cat.rows) != 0 || !reflect.DeepEqual(store.rows[forged.ID], forged) || !reflect.DeepEqual(store.rows[collision.ID], collision) {
		t.Fatalf("%+v %v", r, e)
	}
	if len(s.log.(*testLogger).warns) != 2 {
		t.Fatal("ownership warnings missing")
	}
}

func TestInvalidFileDoesNotTouchStorage(t *testing.T) {
	s, store, cat, p := syncFixture(t)
	f := s.file
	f.PricingRule = "invalid"
	if _, e := New(f, Options{}, Deps{store, cat, p, s.log}); e == nil {
		t.Fatal("invalid file accepted")
	}
	if len(store.calls) != 0 || cat.upserts != 0 {
		t.Fatal("storage touched")
	}
}

func TestPartialFailureRetained(t *testing.T) {
	s, store, cat, _ := syncFixture(t)
	old := OverrideRow{ID: overrideID("gone", "model"), Name: "ee-pricing: dashscope/model", ProviderID: "gone", Pattern: "model"}
	store.rows[old.ID] = old
	store.fail = "delete"
	r, e := s.Sync(context.Background())
	if e == nil || r.Created != 1 || len(store.rows) != 2 || cat.upserts != 0 {
		t.Fatalf("%+v %v", r, e)
	}
	store.fail = ""
	r, e = s.Sync(context.Background())
	if e != nil || r.Unchanged != 1 || r.Deleted != 1 {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestConfigErrorClassification(t *testing.T) {
	s, store, cat, p := syncFixture(t)
	for _, rate := range []float64{0, -1} {
		if _, e := New(s.file, Options{USDToCNY: &rate}, Deps{store, cat, p, s.log}); !errors.Is(e, ErrConfig) {
			t.Fatalf("rate: %v", e)
		}
	}
	if _, e := New(s.file, Options{VendorMap: map[string]string{"q": "unknown"}}, Deps{store, cat, p, s.log}); !errors.Is(e, ErrConfig) {
		t.Fatalf("mapping: %v", e)
	}
	s.file.Vendors = append(s.file.Vendors, Vendor{ID: "overlap", EndpointHosts: []string{"aliyuncs.com"}})
	if _, e := s.Sync(context.Background()); !errors.Is(e, ErrConfig) {
		t.Fatalf("multiple matches: %v", e)
	}
	if len(store.calls) != 0 || cat.upserts != 0 {
		t.Fatal("configuration error touched storage")
	}
}
