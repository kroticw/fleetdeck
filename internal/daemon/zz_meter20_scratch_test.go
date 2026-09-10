package daemon

// Scratch CONTROL for the flap probe: same viewer, same sampling, but NO
// polling. If the width flaps here too, the flap in zz_meter19 is not the
// panel's doing and that finding is void.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMeterWidthFlapControl(t *testing.T) {
	short := os.Getenv("METER_SHORT")
	if short == "" || os.Getenv("METER_TTY") == "" {
		t.Skip("set METER_SHORT and METER_TTY")
	}
	c := liveClient(t)
	proto, err := c.ensureProto(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	key, _ := c.keyFunc()

	viewer := holdAttach(t, c, proto, key, short, 190, 45)
	defer viewer.Close()

	counts := map[int]int{}
	var seq []string
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		_, cols := ttySize(t)
		counts[cols]++
		if len(seq) == 0 || seq[len(seq)-1] != fmt.Sprint(cols) {
			seq = append(seq, fmt.Sprint(cols))
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("CONTROL, no polling, width samples over ~8s: %v", counts)
	t.Logf("CONTROL transitions: %s", strings.Join(seq, " -> "))
}
