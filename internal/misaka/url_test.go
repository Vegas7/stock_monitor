package misaka

import "testing"

func TestParseConsoleURL(t *testing.T) {
	r, p, i, err := ParseConsoleURL("https://app.misaka.io/iaas/vm/create/nrt04/s3n-1c1g/debian-13")
	if err != nil || r != "nrt04" || p != "s3n-1c1g" || i != "debian-13" {
		t.Fatal(r, p, i, err)
	}
	r, p, i, err = ParseConsoleURL("https://app.misaka.io/iaas/vm/create/nrt04/s3n-1c1g")
	if err != nil || r != "nrt04" || p != "s3n-1c1g" || i != "" {
		t.Fatal(r, p, i, err)
	}
	for _, bad := range []string{"https://evil.example/iaas/vm/create/a/b", "https://app.misaka.io/iaas/vm/", "not a url"} {
		if _, _, _, err := ParseConsoleURL(bad); err == nil {
			t.Fatal("accepted", bad)
		}
	}
}
