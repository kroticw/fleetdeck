package daemon

// Scratch: what does a SUCCESSFUL attach header carry — is the session's own
// geometry in it anywhere?

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestMeterAttachHeaderContents(t *testing.T) {
	short := os.Getenv("METER_SHORT")
	if short == "" {
		t.Skip("set METER_SHORT")
	}
	c := liveClient(t)
	proto, err := c.ensureProto(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	key, _ := c.keyFunc()
	hdr := attachRaw(t, c, proto, map[string]any{
		"proto": proto, "op": "attach", "short": short, "cols": 96, "rows": 28, "auth": key,
	}, 600*time.Millisecond)
	t.Logf("successful attach header (asked 96x28):\n%s", hdr)
}
