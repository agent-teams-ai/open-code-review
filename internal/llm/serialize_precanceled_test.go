// SPDX-License-Identifier: MIT
// Copyright 2026 iliya

package llm

import (
	"context"
	"errors"
	"testing"
)

type preCanceledSerialProbe struct{ calls int }

func (p *preCanceledSerialProbe) CompletionsWithCtx(ctx context.Context, _ ChatRequest) (*ChatResponse, error) {
	p.calls++
	return nil, context.Canceled
}

// A completed cancellation and a free permit are both ready select cases.
// Regardless of which wins, the inner client must never be entered. Repeat
// across both contexts to exercise that scheduler choice without timing sleeps.
func TestSerializingClientPreCanceledNeverEntersInner(t *testing.T) {
	for _, axis := range []string{"root", "caller"} {
		t.Run(axis, func(t *testing.T) {
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			root, caller := context.Background(), context.Background()
			if axis == "root" {
				root = canceled
			} else {
				caller = canceled
			}
			probe := &preCanceledSerialProbe{}
			client := NewSerializingClient(probe, root)
			for attempt := 0; attempt < 1024; attempt++ {
				_, err := client.CompletionsWithCtx(caller, ChatRequest{})
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v, want context.Canceled", err)
				}
				if probe.calls != 0 {
					t.Fatalf("inner client entered after %s cancellation", axis)
				}
			}
		})
	}
}
