package codexruntime

import (
	"context"
	"errors"
)

// Model is one model Codex offers, as the settings page shows it.
type Model struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Default       bool     `json:"default,omitempty"`
	Efforts       []string `json:"efforts"`
	DefaultEffort string   `json:"defaultEffort,omitempty"`
}

// maxModelPages bounds a list whose cursor never ends.
const maxModelPages = 20

// ListModels asks Codex's local app-server at socket for the models it
// offers, hidden ones left out.
func ListModels(ctx context.Context, socket string) ([]Model, error) {
	c, err := openNativeControl(ctx, socket, func(nativeMessage) {})
	if err != nil {
		return nil, err
	}
	defer c.Close()
	var out []Model
	params := map[string]any{}
	for range maxModelPages {
		var page struct {
			Data []struct {
				ID            string `json:"id"`
				DisplayName   string `json:"displayName"`
				IsDefault     bool   `json:"isDefault"`
				Hidden        bool   `json:"hidden"`
				DefaultEffort string `json:"defaultReasoningEffort"`
				Efforts       []struct {
					Effort string `json:"reasoningEffort"`
				} `json:"supportedReasoningEfforts"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := c.call(ctx, "model/list", params, &page); err != nil {
			return nil, err
		}
		for _, m := range page.Data {
			if m.Hidden {
				continue
			}
			model := Model{ID: m.ID, Name: m.DisplayName, Default: m.IsDefault, DefaultEffort: m.DefaultEffort, Efforts: []string{}}
			for _, e := range m.Efforts {
				model.Efforts = append(model.Efforts, e.Effort)
			}
			out = append(out, model)
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return out, nil
		}
		params = map[string]any{"cursor": *page.NextCursor}
	}
	return out, errors.New("codex model/list did not end")
}
