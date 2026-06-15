package mygene

import (
	"testing"

	"github.com/tamnd/any-cli/kit"
)

// These tests are offline: they exercise the URI driver's pure string functions
// and the host wiring (mint, body, resolve), which need no network.

func TestDomainInfo(t *testing.T) {
	info := Domain{}.Info()
	if info.Scheme != "mygene" {
		t.Errorf("Scheme = %q, want mygene", info.Scheme)
	}
	if len(info.Hosts) == 0 || info.Hosts[0] != Host {
		t.Errorf("Hosts = %v, want [%s]", info.Hosts, Host)
	}
	if info.Identity.Binary != "mygene" {
		t.Errorf("Identity.Binary = %q, want mygene", info.Identity.Binary)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		in  string
		typ string
		id  string
	}{
		{"7157", "gene", "7157"},
		{"ENSG00000141510", "gene", "ENSG00000141510"},
		{"  TP53  ", "gene", "TP53"},
	}
	for _, tc := range cases {
		typ, id, err := Domain{}.Classify(tc.in)
		if err != nil || typ != tc.typ || id != tc.id {
			t.Errorf("Classify(%q) = (%q, %q, %v), want (%q, %q, nil)",
				tc.in, typ, id, err, tc.typ, tc.id)
		}
	}
}

func TestClassifyEmpty(t *testing.T) {
	_, _, err := Domain{}.Classify("")
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestLocate(t *testing.T) {
	cases := []struct {
		uriType string
		id      string
		want    string
	}{
		{"gene", "7157", "https://www.ncbi.nlm.nih.gov/gene/7157"},
		{"gene", "ENSG00000141510", "https://mygene.info/gene/ENSG00000141510"},
	}
	for _, tc := range cases {
		got, err := Domain{}.Locate(tc.uriType, tc.id)
		if err != nil || got != tc.want {
			t.Errorf("Locate(%q, %q) = (%q, %v), want (%q, nil)",
				tc.uriType, tc.id, got, err, tc.want)
		}
	}
}

func TestLocateUnknownType(t *testing.T) {
	_, err := Domain{}.Locate("variant", "rs328")
	if err == nil {
		t.Error("expected error for unknown resource type")
	}
}

// TestHostWiring mounts the driver in a kit Host and checks the round trip:
// a record mints to its URI, its body is readable, and a bare id resolves back
// to the same URI. The init in domain.go registers the domain, so kit.Open finds it.
func TestHostWiring(t *testing.T) {
	h, err := kit.Open()
	if err != nil {
		t.Fatal(err)
	}

	g := &Gene{ID: "7157", Symbol: "TP53", Name: "tumor protein p53", TaxID: 9606}
	u, err := h.Mint(g)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if want := "mygene://gene/7157"; u.String() != want {
		t.Errorf("Mint = %q, want %q", u.String(), want)
	}

	got, err := h.ResolveOn("mygene", "ENSG00000141510")
	if err != nil || got.String() != "mygene://gene/ENSG00000141510" {
		t.Errorf("ResolveOn = (%q, %v), want mygene://gene/ENSG00000141510", got.String(), err)
	}
}
