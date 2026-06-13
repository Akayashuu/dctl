package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestResponderSelection(t *testing.T) {
	noop := func(ctx context.Context, m dctlMessage) (string, error) { return "x", nil }
	if _, ok := newResponder(context.Background(), false, "foo", "", noop).(*oneShotResponder); !ok {
		t.Fatal("stream=false should yield oneShotResponder")
	}
	if _, ok := newResponder(context.Background(), true, "claude", "", noop).(*streamResponder); !ok {
		t.Fatal("stream=true should yield streamResponder")
	}
}

func TestStreamArgv(t *testing.T) {
	got := streamArgv([]string{"claude", "--permission-mode", "acceptEdits"}, "claude-haiku", "sess-9")
	want := []string{
		"claude", "--permission-mode", "acceptEdits",
		"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--model", "claude-haiku",
		"--resume", "sess-9",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("argv =\n  %v\nwant\n  %v", got, want)
	}

	// No model / no resume → those flags are omitted.
	bare := streamArgv([]string{"claude"}, "", "")
	for _, f := range bare {
		if f == "--model" || f == "--resume" {
			t.Fatalf("did not expect %q in %v", f, bare)
		}
	}
}

func TestStreamSessionSend(t *testing.T) {
	// Fake "process": reads one user line from stdin, replies with a canned result.
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	go func() {
		br := bufio.NewReader(stdinR)
		if _, err := br.ReadBytes('\n'); err != nil {
			return
		}
		io.WriteString(stdoutW,
			`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`+"\n"+
				`{"type":"result","subtype":"success","is_error":false,"result":"hello back","total_cost_usd":0.002,"session_id":"abc"}`+"\n")
		stdoutW.Close()
	}()

	s := newStreamSession(stdinW, stdoutR)
	tr, err := s.Send("hello")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Text != "hello back" {
		t.Fatalf("text = %q, want 'hello back'", tr.Text)
	}
	if s.sessID != "abc" {
		t.Fatalf("session id not recorded: %q", s.sessID)
	}
}

func TestUserLineShape(t *testing.T) {
	line, err := userLine("hi there")
	if err != nil {
		t.Fatal(err)
	}
	if len(line) == 0 || line[len(line)-1] != '\n' {
		t.Fatalf("expected newline-terminated line, got %q", line)
	}
	var v struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &v); err != nil {
		t.Fatalf("not valid json: %v", err)
	}
	if v.Type != "user" || v.Message.Role != "user" || v.Message.Content != "hi there" {
		t.Fatalf("wrong shape: %+v", v)
	}
}

func TestReadTurnSuccess(t *testing.T) {
	canned := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"sess-1","model":"claude-haiku"}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"}]},"session_id":"sess-1"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"PONG"}]},"session_id":"sess-1"}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"PONG","total_cost_usd":0.0136,"session_id":"sess-1"}`,
	}, "\n") + "\n"

	tr, err := readTurn(bufio.NewReader(strings.NewReader(canned)))
	if err != nil {
		t.Fatal(err)
	}
	if tr.Text != "PONG" {
		t.Fatalf("text = %q, want PONG", tr.Text)
	}
	if tr.CostUSD <= 0 {
		t.Fatalf("cost = %v, want > 0", tr.CostUSD)
	}
	if tr.SessionID != "sess-1" {
		t.Fatalf("session id = %q, want sess-1", tr.SessionID)
	}
	if tr.IsError {
		t.Fatal("did not expect error")
	}
}

func TestReadTurnError(t *testing.T) {
	canned := `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom","session_id":"s"}` + "\n"
	tr, err := readTurn(bufio.NewReader(strings.NewReader(canned)))
	if err != nil {
		t.Fatal(err)
	}
	if !tr.IsError {
		t.Fatal("expected IsError")
	}
	if tr.ErrMsg == "" {
		t.Fatal("expected ErrMsg populated")
	}
}

func TestReadTurnHandlesHugeLine(t *testing.T) {
	huge := strings.Repeat("x", 200_000)
	canned := `{"type":"system","subtype":"init","session_id":"s","blob":"` + huge + `"}` + "\n" +
		`{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s"}` + "\n"
	tr, err := readTurn(bufio.NewReader(strings.NewReader(canned)))
	if err != nil {
		t.Fatalf("huge line should not error: %v", err)
	}
	if tr.Text != "ok" {
		t.Fatalf("text = %q, want ok", tr.Text)
	}
}
