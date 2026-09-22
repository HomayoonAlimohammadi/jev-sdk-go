package jev

import (
	"context"
	"net/http"
)

// ListModels returns the models available to the account. A model's Name is
// accepted by [SystemOneRequest.Model].
//
//	models, err := client.ListModels(ctx)
func (c *Client) ListModels(ctx context.Context, opts ...CallOption) (*ListModelsResponse, error) {
	call, err := applyCallOptions(opts)
	if err != nil {
		return nil, err
	}

	meta, err := c.do(ctx, http.MethodGet, modelsPath, nil, call)
	if err != nil {
		return nil, err
	}
	return decodeListModels(meta, c.endpoint(http.MethodGet, modelsPath))
}
