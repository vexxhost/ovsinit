package verifier

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Mock verifier for testing
type mockVerifier struct {
	name       string
	shouldFail bool
	delay      time.Duration
}

func (m *mockVerifier) String() string {
	return m.name
}

func (m *mockVerifier) Verify(ctx context.Context) error {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if m.shouldFail {
		return assert.AnError
	}
	return nil
}

func TestRun(t *testing.T) {
	tests := []struct {
		name      string
		verifiers []Verifier
		timeout   time.Duration
		wantErr   bool
	}{
		{
			name:      "no verifiers",
			verifiers: []Verifier{},
			timeout:   1 * time.Second,
			wantErr:   false,
		},
		{
			name: "single verifier success",
			verifiers: []Verifier{
				&mockVerifier{name: "test1", shouldFail: false},
			},
			timeout: 1 * time.Second,
			wantErr: false,
		},
		{
			name: "multiple verifiers success",
			verifiers: []Verifier{
				&mockVerifier{name: "test1", shouldFail: false},
				&mockVerifier{name: "test2", shouldFail: false},
				&mockVerifier{name: "test3", shouldFail: false},
			},
			timeout: 1 * time.Second,
			wantErr: false,
		},
		{
			name: "one verifier fails",
			verifiers: []Verifier{
				&mockVerifier{name: "test1", shouldFail: false},
				&mockVerifier{name: "test2", shouldFail: true},
				&mockVerifier{name: "test3", shouldFail: false},
			},
			timeout: 1 * time.Second,
			wantErr: true,
		},
		{
			name: "all verifiers fail",
			verifiers: []Verifier{
				&mockVerifier{name: "test1", shouldFail: true},
				&mockVerifier{name: "test2", shouldFail: true},
			},
			timeout: 1 * time.Second,
			wantErr: true,
		},
		{
			name: "timeout",
			verifiers: []Verifier{
				&mockVerifier{name: "test1", delay: 2 * time.Second},
			},
			timeout: 100 * time.Millisecond,
			wantErr: true,
		},
		{
			name: "parallel execution",
			verifiers: []Verifier{
				&mockVerifier{name: "test1", delay: 50 * time.Millisecond},
				&mockVerifier{name: "test2", delay: 50 * time.Millisecond},
				&mockVerifier{name: "test3", delay: 50 * time.Millisecond},
			},
			timeout: 100 * time.Millisecond,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), tt.timeout)
			defer cancel()

			err := Run(ctx, tt.verifiers...)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
