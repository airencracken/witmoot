// SPDX-License-Identifier: AGPL-3.0-or-later

package contrib

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airencracken/comfylib/proxyconfig"
)

// Each shipped example renders, every example host and upstream is replaced,
// and the header names Witmoot's own settings.
func TestProxyConfigurations(t *testing.T) {
	spec := ProxySpec()
	for _, server := range []string{"caddy", "nginx", "apache"} {
		t.Run(server, func(t *testing.T) {
			config, err := proxyconfig.Render(spec, proxyconfig.Options{Server: server, Domain: "photos.example.net", Upstream: "[::1]:9100"})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"photos.example.net", "[::1]:9100", "# WITMOOT_ADDR=[::1]:9100", "# WITMOOT_BASE_URL=https://photos.example.net", "# WITMOOT_SECURE_COOKIES=true", "# WITMOOT_TRUSTED_PROXIES=127.0.0.1/32,::1/128", "/etc/conf.d/witmoot", "/etc/witmoot/witmoot.env"} {
				if !strings.Contains(config, want) {
					t.Errorf("missing %q in generated %s config", want, server)
				}
			}
			if strings.Contains(config, spec.ExampleDomain) || strings.Contains(config, spec.DefaultUpstream) {
				t.Fatal("generated config contains the original example's host or upstream")
			}
			if server != "caddy" && !strings.Contains(config, `"/etc/letsencrypt/live/photos.example.net/fullchain.pem"`) {
				t.Fatal("default certificate path does not follow the domain")
			}
		})
	}
}

// The examples on disk, which the packages install, are the ones embedded.
func TestEmbeddedExamplesMatchTheShippedFiles(t *testing.T) {
	for _, path := range []string{"caddy/Caddyfile", "nginx/witmoot.conf", "apache/witmoot.conf"} {
		embedded, err := examples.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		shipped, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(embedded, shipped) {
			t.Fatalf("%s differs from the embedded copy: %v", path, err)
		}
		// Every value proxy-config substitutes appears, so none is left
		// pointing at the example host or port.
		for _, value := range []string{"board.example.org", "127.0.0.1:8082"} {
			if !bytes.Contains(shipped, []byte(value)) {
				t.Errorf("%s does not use %s", path, value)
			}
		}
	}
}

func TestGeneratedCaddyConfigurationAdapts(t *testing.T) {
	binary := os.Getenv("CADDY_BINARY")
	if binary == "" {
		var err error
		binary, err = exec.LookPath("caddy")
		if err != nil {
			t.Skip("install Caddy to validate its generated configuration")
		}
	}
	config, err := proxyconfig.Render(ProxySpec(), proxyconfig.Options{Server: "caddy", Domain: "photos.example.net", Upstream: "[::1]:9100"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(binary, "adapt", "--adapter", "caddyfile", "--config", path).CombinedOutput(); err != nil {
		t.Fatalf("Caddy rejected generated configuration: %v\n%s", err, output)
	}
}
