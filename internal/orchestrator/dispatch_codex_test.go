package orchestrator

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/executor"
)

const codexKey = "codex:83879aa5-dc4b-40a3-a5af-613e0733e32a"

func codexCard(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Replace(workCard, "stage: new", "stage: new\nexecutor: codex", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func codexDispatcher(t *testing.T, f *fakeWorker, receipt executor.DeliveryState) (*Dispatcher, string, *[]string) {
	t.Helper()
	d, path := dispatcher(t, f)
	codexCard(t, path)
	var ops []string
	f.codex = &executor.Adapter{
		ID: executor.Codex,
		Start: func(_ context.Context, req executor.StartRequest) (executor.Session, error) {
			f.steps = append(f.steps, "codex-start:"+req.CWD+":"+req.Card+":"+req.Name)
			return executor.Session{Key: codexKey}, nil
		},
		Send: func(_ context.Context, key executor.Key, m executor.Message) (executor.Receipt, error) {
			f.steps = append(f.steps, "codex-send:"+string(key)+":"+m.Text)
			ops = append(ops, m.OperationID)
			return executor.Receipt{OperationID: m.OperationID, State: receipt}, nil
		},
	}
	d.Executors = f.registry()
	return d, path, &ops
}

func TestDispatchHandsACodexCardToCodexInTheSameOrder(t *testing.T) {
	f := newWorker()
	d, path, ops := codexDispatcher(t, f, executor.Accepted)
	res, err := d.Dispatch(context.Background(), Work{Card: path})
	if err != nil || !res.OK || res.Session != codexKey {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	want := []string{"codex-start:", "set:T-042-card.md:session=" + codexKey + ":was=", "set:T-042-card.md:stage=active:was=new", "codex-send:" + codexKey + ":"}
	if len(f.steps) != len(want) {
		t.Fatalf("steps %v", f.steps)
	}
	for i, prefix := range want {
		if !strings.HasPrefix(f.steps[i], prefix) {
			t.Fatalf("step %d = %q, want %q…", i, f.steps[i], prefix)
		}
	}
	if !strings.Contains(f.steps[0], ":"+path+":") || !strings.Contains(f.steps[3], Task("", path)) {
		t.Fatalf("codex start or task lacks the card: %v", f.steps)
	}
	if len(*ops) != 1 || len((*ops)[0]) < 8 {
		t.Fatalf("the task needs one operation ID: %v", *ops)
	}
}

func TestDispatchNeverResendsAnUnconfirmedCodexTask(t *testing.T) {
	f := newWorker()
	d, path, ops := codexDispatcher(t, f, executor.DeliveryUnknown)
	res, err := d.Dispatch(context.Background(), Work{Card: path})
	if err != nil || res.OK || len(*ops) != 1 {
		t.Fatalf("res=%+v err=%v ops=%v", res, err, *ops)
	}
	last := res.Steps[len(res.Steps)-1]
	if last.Name != "task" || !strings.Contains(last.Error, "not confirmed") {
		t.Fatalf("unknown delivery step: %+v", last)
	}
}

func TestDispatchRefusesCodexWhenItIsNotConfigured(t *testing.T) {
	f := newWorker()
	d, path := dispatcher(t, f)
	codexCard(t, path)
	if _, err := d.Dispatch(context.Background(), Work{Card: path}); !errors.Is(err, ErrCannotStart) || len(f.steps) != 0 {
		t.Fatalf("err=%v steps=%v", err, f.steps)
	}
}
