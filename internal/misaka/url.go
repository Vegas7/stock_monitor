package misaka

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseConsoleURL reads region/plan/image from a console "create VM" link.
func ParseConsoleURL(u string) (region, plan, image string, err error) {
	p, err := url.Parse(strings.TrimSpace(u))
	if err != nil || p.Host != "app.misaka.io" {
		return "", "", "", fmt.Errorf("not an app.misaka.io link: %q", u)
	}
	rest, ok := strings.CutPrefix(strings.Trim(p.Path, "/"), "iaas/vm/create/")
	if !ok {
		return "", "", "", fmt.Errorf("expected a /iaas/vm/create/<region>/<plan>[/<image>] link")
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", fmt.Errorf("link has no region and plan")
	}
	if len(parts) > 2 {
		image = parts[2]
	}
	return parts[0], parts[1], image, nil
}
