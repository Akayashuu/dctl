// internal/transport/stub.go
package transport

import (
	"context"
	"encoding/json"
)

// Call is one captured request.
type Call struct {
	Method string
	Path   string
	Body   any
}

// Stub is an in-memory Doer for tests: it records calls and replays canned
// JSON responses or errors in FIFO order. The zero value is unusable; use NewStub.
type Stub struct {
	calls   []Call
	replies []string
	err     error
}

// NewStub builds an empty stub.
func NewStub() *Stub { return &Stub{} }

// Reply queues a canned JSON response body (consumed in order by Do).
func (s *Stub) Reply(json string) *Stub { s.replies = append(s.replies, json); return s }

// Fail makes the next (and every) Do return err.
func (s *Stub) Fail(err error) *Stub { s.err = err; return s }

// Last returns the most recently captured call.
func (s *Stub) Last() Call { return s.calls[len(s.calls)-1] }

// Calls returns every captured call in order.
func (s *Stub) Calls() []Call { return s.calls }

func (s *Stub) Do(ctx context.Context, method, path string, body, out any) error {
	s.calls = append(s.calls, Call{Method: method, Path: path, Body: body})
	if s.err != nil {
		return s.err
	}
	if out == nil || len(s.replies) == 0 {
		return nil
	}
	reply := s.replies[0]
	s.replies = s.replies[1:]
	return json.Unmarshal([]byte(reply), out)
}
