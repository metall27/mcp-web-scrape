package geo

import (
	"context"
	"os"
	"testing"
	"time"
)

// Live integration check against real providers (guarded by GEO_LIVE=1 so
// offline runs skip it). Verifies the production chain end-to-end.
func TestGeoChainLiveResolve(t *testing.T) {
	if os.Getenv("GEO_LIVE") == "" {
		t.Skip("set GEO_LIVE=1 to run the live geo check")
	}
	r := NewCachedResolver(NewGeoResolver(true, ""), time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	l1, err := r.Resolve(ctx, "")
	if err != nil {
		t.Fatalf("live resolve failed: %v", err)
	}
	if l1.Country == "" || l1.Timezone == "" || l1.Language == "" {
		t.Fatalf("incomplete locale: %+v", l1)
	}
	t.Logf("resolved: %+v", l1)

	l2, err := r.Resolve(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if l2.Source != "cache" {
		t.Errorf("second resolve source = %q, want cache", l2.Source)
	}
}

// Live check of the static (offline) mode — no network by construction.
func TestStaticModeLiveSanity(t *testing.T) {
	r := NewGeoResolver(false, "RU")
	loc, err := r.Resolve(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("static mode: %+v", loc)
	if loc.Source != "static" {
		t.Errorf("source = %q, want static", loc.Source)
	}
}
