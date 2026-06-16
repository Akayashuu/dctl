package dctl

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSecretRedactsButReveals(t *testing.T) {
	s := Secret("super-token")

	for _, verb := range []string{"%v", "%+v", "%s", "%#v"} {
		if got := fmt.Sprintf(verb, s); strings.Contains(got, "super-token") {
			t.Errorf("%s leaked the secret: %q", verb, got)
		}
	}

	if s.Reveal() != "super-token" {
		t.Errorf("Reveal = %q, want the real value", s.Reveal())
	}
}

func TestWebhookTokenRedactedInLogsAndJSON(t *testing.T) {
	w := Webhook{ID: "1", Name: "hook", Token: "secret-token"}

	if got := fmt.Sprintf("%+v", w); strings.Contains(got, "secret-token") {
		t.Errorf("%%+v leaked webhook token: %q", got)
	}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret-token") {
		t.Errorf("json.Marshal leaked webhook token: %s", b)
	}

	// The value still round-trips IN from Discord and is usable via Reveal.
	var in Webhook
	if err := json.Unmarshal([]byte(`{"id":"1","name":"hook","token":"secret-token"}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.Token.Reveal() != "secret-token" {
		t.Errorf("unmarshalled token = %q, want usable value", in.Token.Reveal())
	}
}
