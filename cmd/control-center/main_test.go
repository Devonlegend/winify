package main

import "testing"

func TestRemoteServerID(t *testing.T) {
	cases := map[string]string{
		"http://10.0.0.5:5985/wsman":    "remote-10-0-0-5",
		"https://win.example.com/wsman": "remote-win-example-com",
		"10.0.0.9":                      "remote-10-0-0-9",
	}
	for endpoint, want := range cases {
		if got := remoteServerID(endpoint); got != want {
			t.Errorf("remoteServerID(%q) = %q, want %q", endpoint, got, want)
		}
	}
}

func TestSanitizeID(t *testing.T) {
	if got := sanitizeID("Win.Example.COM"); got != "win-example-com" {
		t.Fatalf("sanitizeID = %q", got)
	}
	if got := sanitizeID("--a--"); got != "a" {
		t.Fatalf("sanitizeID trimming = %q", got)
	}
}
