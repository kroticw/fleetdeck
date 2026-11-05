package codexruntime

import (
	"context"
	"reflect"
	"testing"
)

// The settings page offers the models Codex itself lists, each with the
// efforts it supports, across every page of the list.
func TestListModelsFollowsEveryPage(t *testing.T) {
	f := newFakeNative(t)
	models, err := ListModels(context.Background(), f.socket)
	if err != nil {
		t.Fatal(err)
	}
	want := []Model{
		{ID: "gpt-5.6", Name: "GPT-5.6", Default: true, Efforts: []string{"low", "medium"}, DefaultEffort: "medium"},
		{ID: "gpt-6.1-sol", Name: "GPT-6.1 Sol", Efforts: []string{"high", "xhigh"}, DefaultEffort: "high"},
	}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %+v", models)
	}
}
