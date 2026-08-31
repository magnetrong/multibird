package instance

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDeriveParams(t *testing.T) {
	tests := []struct {
		name string
		inst Instance
		root string
		run  string
		want Params
	}{
		{
			name: "index 0 defaults",
			inst: Instance{Name: "home", Index: 0},
			root: "/home/u/.config/multibird",
			run:  "/var/run/multibird",
			want: Params{
				Dir:        "/home/u/.config/multibird/home",
				TOMLPath:   "/home/u/.config/multibird/home/instance.toml",
				ConfigJSON: "/home/u/.config/multibird/home/config.json",
				LogFile:    "/home/u/.config/multibird/home/daemon.log",
				SocketPath: "/var/run/multibird/home.sock",
				DaemonAddr: "unix:///var/run/multibird/home.sock",
				PIDFile:    "/var/run/multibird/home.pid",
				WGPort:     51900,
			},
		},
		{
			name: "index 3 derives port 51903",
			inst: Instance{Name: "lab", Index: 3},
			root: "/r",
			run:  "/v",
			want: Params{
				Dir: "/r/lab", TOMLPath: "/r/lab/instance.toml",
				ConfigJSON: "/r/lab/config.json", LogFile: "/r/lab/daemon.log",
				SocketPath: "/v/lab.sock", DaemonAddr: "unix:///v/lab.sock",
				PIDFile: "/v/lab.pid", WGPort: 51903,
			},
		},
		{
			name: "explicit port overrides derivation",
			inst: Instance{Name: "lab", Index: 3, WireguardPort: 40000},
			root: "/r", run: "/v",
			want: Params{
				Dir: "/r/lab", TOMLPath: "/r/lab/instance.toml",
				ConfigJSON: "/r/lab/config.json", LogFile: "/r/lab/daemon.log",
				SocketPath: "/v/lab.sock", DaemonAddr: "unix:///v/lab.sock",
				PIDFile: "/v/lab.pid", WGPort: 40000,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.inst.DeriveParams(tt.root, tt.run)
			if got != tt.want {
				t.Errorf("DeriveParams() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Ports must never collide with stock netbird's 51820 default for any sane index.
func TestPortNeverCollidesWithStockNetbird(t *testing.T) {
	for idx := 0; idx < 100; idx++ {
		i := Instance{Name: "x", Index: idx}
		if p := i.DeriveParams("/r", "/v").WGPort; p == 51820 {
			t.Fatalf("index %d derives stock netbird's port 51820", idx)
		}
	}
}

func TestSetupKeyRedaction(t *testing.T) {
	i := Instance{Name: "home", ManagementURL: "https://m", SetupKey: "SECRET-KEY-VALUE"}
	if s := i.String(); strings.Contains(s, "SECRET-KEY-VALUE") {
		t.Errorf("String() leaks the setup key: %s", s)
	}
	b, err := json.Marshal(i)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "SECRET-KEY-VALUE") {
		t.Errorf("JSON leaks the setup key: %s", b)
	}
	if !strings.Contains(string(b), `"has_setup_key":true`) {
		t.Errorf("JSON should indicate a key exists: %s", b)
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
	}{
		{"home", true}, {"lab-2", true}, {"0x", true},
		{"", false}, {"Home", false}, {"has space", false},
		{"../evil", false}, {"way-too-long-name-for-an-instance-x", false},
		{"-leading", false},
	}
	for _, tt := range tests {
		if err := ValidateName(tt.name); (err == nil) != tt.ok {
			t.Errorf("ValidateName(%q): got err=%v, want ok=%v", tt.name, err, tt.ok)
		}
	}
}

func TestStoreRoundTripAndPerms(t *testing.T) {
	s := &Store{Root: t.TempDir(), RunDir: "/v"}
	in := &Instance{Name: "home", ManagementURL: "https://m", SetupKey: "k", Index: 0}
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	out, err := s.Load("home")
	if err != nil {
		t.Fatal(err)
	}
	if *out != *in {
		t.Errorf("round trip mismatch: %+v != %+v", out, in)
	}
	// credentials: 0600 file in 0700 dir
	p := s.params(in)
	assertMode(t, p.TOMLPath, 0o600)
	assertMode(t, p.Dir, 0o700)

	idx, err := s.NextIndex()
	if err != nil || idx != 1 {
		t.Errorf("NextIndex = %d, %v; want 1", idx, err)
	}
}

func assertMode(t *testing.T, path string, want uint32) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := uint32(fi.Mode().Perm()); got != want {
		t.Errorf("%s has mode %o, want %o", path, got, want)
	}
}
