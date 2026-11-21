package cache

import (
	"context"
	"testing"
	"time"
)

func TestDirRoundTripAndExpiry(t *testing.T) {
	ctx := context.Background()
	d, err := NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := d.Get(ctx, "missing"); ok {
		t.Fatal("missing key reported as present")
	}
	if err := d.Set(ctx, "sandbox:txn:ABC", []byte(`{"status":"success"}`), 0); err != nil {
		t.Fatal(err)
	}
	// A second instance on the same directory (another process) sees the value.
	other, _ := NewDir(d.path)
	got, ok, err := other.Get(ctx, "sandbox:txn:ABC")
	if err != nil || !ok || string(got) != `{"status":"success"}` {
		t.Fatalf("got %q %v %v", got, ok, err)
	}

	if err := d.Set(ctx, "short", []byte("x"), time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, ok, _ := d.Get(ctx, "short"); ok {
		t.Fatal("expired key still present")
	}

	for want := int64(1); want <= 3; want++ {
		if n, err := d.Incr(ctx, "counter", time.Minute); err != nil || n != want {
			t.Fatalf("Incr = %d, %v; want %d", n, err, want)
		}
	}
	if err := d.Delete(ctx, "counter", "never-existed"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := d.Get(ctx, "counter"); ok {
		t.Fatal("deleted key still present")
	}
}
