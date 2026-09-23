package typesafe

import (
	"context"
	"net/http"
)

// Models is the models API resource, reached through [Client.Models].
type Models struct {
	client *Client
}

// List returns the models and aliases available to the account. Use a model's
// name with [WithModel] or [WithDefaultModel].
func (m *Models) List(ctx context.Context, options ...RequestOption) ([]ModelMetadata, error) {
	resolved, err := m.client.resolve(options)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.send(ctx, http.MethodGet, modelsPath, nil, resolved)
	if err != nil {
		return nil, err
	}

	var wire struct {
		Models []ModelMetadata `json:"models"`
	}
	if err := resp.unmarshal("", resp.body, &wire); err != nil {
		return nil, err
	}
	if wire.Models == nil {
		return nil, resp.invalid("models", nil)
	}
	return wire.Models, nil
}
