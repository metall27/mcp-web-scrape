package geo

import (
	"context"
	"os"
	"testing"
	"time"
)

// Live integration check against the real ipinfo.io (guarded by
// GEO_LIVE=1 so CI/offline runs skip it). Verifies the whole chain the
// production code uses: ipinfoResolver -> mapping -> cache.
func TestIPInfoLiveResolve(t *testing.T) {
	if os.Getenv("GEO_LIVE") == "" {
		t.Skip("set GEO_LIVE=1 to run the live ipinfo check")
	}
	r := NewCachedResolver(NewIPInfoResolver(), time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
