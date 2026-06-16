package dctl

import (
	"context"
	"strings"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

func TestMessagesReadEscapesAfterQuery(t *testing.T) {
	s := transport.NewStub().Reply(`[]`)
	if _, err := msgs(s, "def").Read(context.Background(), "c", 50, "0&limit=999"); err != nil {
		t.Fatal(err)
	}
	path := s.Last().Path
	if strings.Contains(path, "0&limit=999") {
		t.Errorf("after not escaped: %s", path)
	}
	if !strings.Contains(path, "after=0%26limit%3D999") {
		t.Errorf("want encoded after, got %s", path)
	}
}

func TestMessagesDeleteEscapesPathSegments(t *testing.T) {
	s := transport.NewStub()
	if err := msgs(s, "").Delete(context.Background(), "c", "../../evil"); err != nil {
		t.Fatal(err)
	}
	path := s.Last().Path
	if strings.Contains(path, "../../evil") {
		t.Errorf("messageID not escaped: %s", path)
	}
	if !strings.Contains(path, "..%2F..%2Fevil") {
		t.Errorf("want encoded messageID, got %s", path)
	}
}

func TestReactionsEscapeIDs(t *testing.T) {
	s := transport.NewStub()
	r := &Reactions{rt: s, def: &defaults{}}
	if err := r.Add(context.Background(), "c", "../../evil", "x"); err != nil {
		t.Fatal(err)
	}
	if path := s.Last().Path; strings.Contains(path, "../../evil") {
		t.Errorf("messageID not escaped: %s", path)
	}
}
