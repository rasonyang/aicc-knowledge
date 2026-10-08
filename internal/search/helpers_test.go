// SPDX-License-Identifier: Apache-2.0

package search_test

import (
	"testing"

	"github.com/rasonyang/aicc-knowledge/internal/embed"
	"github.com/rasonyang/aicc-knowledge/internal/search"
)

// refusedEmbedder is a real TEI client pointed at a closed port.
func refusedEmbedder(t *testing.T) search.Embedder {
	t.Helper()
	c, err := embed.New(embed.Config{BaseURL: "http://127.0.0.1:1", Dimensions: 1024, MaxBatch: 8})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
