package deployment

import "testing"

func TestParseWinRMEndpoint(t *testing.T) {
	cases := []struct {
		endpoint string
		host     string
		port     int
		https    bool
		wantErr  bool
	}{
		{"https://10.0.0.5:5986/wsman", "10.0.0.5", 5986, true, false},
		{"http://10.0.0.5:5985/wsman", "10.0.0.5", 5985, false, false},
		{"https://win.example.com/wsman", "win.example.com", 5986, true, false},
		{"http://win.example.com/wsman", "win.example.com", 5985, false, false},
		{"https://10.0.0.5:8443/wsman", "10.0.0.5", 8443, true, false},
		{"not a url", "", 0, false, true},
		{"", "", 0, false, true},
	}
	for _, tc := range cases {
		host, port, https, err := parseWinRMEndpoint(tc.endpoint)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseWinRMEndpoint(%q) = nil error, want error", tc.endpoint)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseWinRMEndpoint(%q): %v", tc.endpoint, err)
			continue
		}
		if host != tc.host || port != tc.port || https != tc.https {
			t.Errorf("parseWinRMEndpoint(%q) = (%q,%d,%v), want (%q,%d,%v)",
				tc.endpoint, host, port, https, tc.host, tc.port, tc.https)
		}
	}
}
