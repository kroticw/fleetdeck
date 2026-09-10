package daemon

// Scratch: with a wide viewer attached, does the panel's once-a-second polling
// make the session's real geometry FLAP between the viewer's width and 80?
// Sampled from the kernel every 100ms.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMeterWidthFlap(t *testing.T) {
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
	_, w := ttySize(t)
	t.Logf("viewer(190) attached, kernel width = %d", w)

	stop := make(chan struct{})
	samples := make(chan int, 400)
	go func() {
		for {
			select {
			case <-stop:
				close(samples)
				return
			default:
			}
			_, cols := ttySize(t)
			samples <- cols
			time.Sleep(100 * time.Millisecond)
		}
	}()

	// The screen tab: one ReadScreen, then a 1000ms gap, five times over.
	for i := 0; i < 5; i++ {
		_ = c.ReadScreen(context.Background(), short, 64<<10)
		time.Sleep(1000 * time.Millisecond)
	}
	close(stop)

	counts := map[int]int{}
	var seq []string
	for s := range samples {
		counts[s]++
		if len(seq) == 0 || seq[len(seq)-1] != fmt.Sprint(s) {
			seq = append(seq, fmt.Sprint(s))
		}
	}
	t.Logf("width samples over ~8s of polling: %v", counts)
	t.Logf("transitions: %s", strings.Join(seq, " -> "))
}
